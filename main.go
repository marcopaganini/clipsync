// clipsync - Synchronize clipboard across machines.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fredli74/lockfile"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	// Config file.
	configFile = "~/.config/clipsync/config"
)

// globalConfig holds the user global configurations as requested in the
// command line or in the configuration file.
type globalConfig struct {
	cafile       *string
	debug        *bool
	encpassfile  *string
	mqttdebug    *bool
	nocolors     *bool
	password     *string
	passwordfile *string
	redactlevel  *int
	server       *string
	topic        *string
	user         *string
	verbose      *bool
}

// BuildVersion Holds the current git HEAD version number.
// This is filled in by the build process (make).
var BuildVersion string

// The redact object is used by other functions in this namespace.
var redact redactType

func newBroker(cfg globalConfig, handler func(client mqtt.Client, msg mqtt.Message)) (mqtt.Client, error) {
	tlsconfig, err := newTLSConfig(*cfg.cafile)
	if err != nil {
		return nil, err
	}
	opts := mqtt.NewClientOptions()
	opts.AddBroker(*cfg.server)

	// Client ID must be unique.
	id := uuid.New()
	clientID := "clipsync-" + id.String()
	opts.SetClientID(clientID)
	log.Debugf("Set MQTT Client ID to %v", clientID)

	opts.SetKeepAlive(4 * time.Second)
	opts.SetTLSConfig(tlsconfig)
	opts.SetPingTimeout(2 * time.Second)
	opts.SetAutoReconnect(true)

	if *cfg.user != "" {
		opts.SetUsername(*cfg.user)
	}
	if *cfg.password != "" {
		opts.SetPassword(*cfg.password)
	}

	// If handler is present, assume we'll subscribe to a topic. In this case,
	// set OnConnectHandler to re-subscribe every time we have a connection.
	// This, together with SetAutoReconnect guarantees that we'll keep
	// receiving messages from the topic after an automatic reconnect.
	if handler != nil {
		opts.SetOnConnectHandler(func(onconn mqtt.Client) {
			log.Debugf("Connection detected. Subscribing to topic: %q", *cfg.topic)
			if token := onconn.Subscribe(*cfg.topic, 0, handler); token.Wait() && token.Error() != nil {
				log.Errorf("Unable to subscribe to topic %s: %v", *cfg.topic, token.Error())
			}
		})
	}

	c := mqtt.NewClient(opts)
	if token := c.Connect(); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	return c, nil
}

func newTLSConfig(cafile string) (*tls.Config, error) {
	// Create tls.Config with desired tls properties
	ret := &tls.Config{
		ClientAuth: tls.NoClientCert,
		// ClientCAs = certs used to validate client cert.
		ClientCAs: nil,
		// InsecureSkipVerify = Cert contents must match server, IP, host, etc.
		//InsecureSkipVerify: true,
	}
	if cafile != "" {
		certpool := x509.NewCertPool()
		pemCerts, err := os.ReadFile(cafile)
		if err == nil {
			certpool.AppendCertsFromPEM(pemCerts)
		}
		ret.RootCAs = certpool
	}
	return ret, nil
}

// singleInstanceOrDie guarantees that this is the only instance of
// this program using the specified lockfile. Caller must call
// Unlock on the returned lock once it's not needed anymore.
func singleInstanceOrDie(lckfile string) *lockfile.LockFile {
	lock, err := lockfile.Lock(lckfile)
	if err != nil {
		log.Fatalf("Another instance is already running.")
	}
	return lock
}

// pastecmd prints the first message from the server (all messages are sent
// with persist).
func pastecmd(cfg globalConfig, cryptPassword []byte) error {
	log.Debug("Got paste command")
	ch := make(chan string)

	broker, err := newBroker(cfg, func(client mqtt.Client, msg mqtt.Message) {
		var err error

		data := string(msg.Payload())

		if len(cryptPassword) > 0 {
			data, err = decrypt64(data, cryptPassword)
			if err != nil {
				log.Error(err)
				data = ""
			}
		}

		log.Debugf("Received from server: %s", redact.redact(data))
		ch <- data
	})
	if err != nil {
		return fmt.Errorf("Unable to connect to broker: %v", err)
	}

	// Wait for read return
	spub := <-ch
	fmt.Print(spub)
	broker.Disconnect(1)

	return nil
}

// copycmd reads the stdin and sends it to the broker (server).
func copycmd(cfg globalConfig, cryptPassword []byte, filter bool) error {
	log.Debug("Got copy command")
	broker, err := newBroker(cfg, nil)
	if err != nil {
		return fmt.Errorf("Unable to connect to broker: %v", err)
	}
	pub, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("Unable to read data from stdin: %v", err)
	}
	defer broker.Disconnect(1)
	spub := string(pub)

	log.Debugf("Sending from stdin to broker: %s", redact.redact(spub))
	publish(broker, *cfg.topic, spub, cryptPassword)
	if filter {
		fmt.Print(spub)
	}
	return nil
}

// clientcmd activates "client" mode, syncing the local clipboard to the server
// and vice-versa. This function will only return in case of error.
func clientcmd(cfg globalConfig, cryptPassword []byte, polltime int, chromequirk, syncsel bool) error {
	// Client mode only makes sense if the DISPLAY environment
	// variable is set (otherwise we don't have a clipboard to sync).
	if os.Getenv("DISPLAY") == "" {
		return fmt.Errorf("Client mode requires the DISPLAY variable to be set")
	}

	log.Infof("Starting client, server: %s", *cfg.server)
	cli := newClient(*cfg.topic, syncsel, cryptPassword)
	broker, err := newBroker(cfg, cli.subHandler)
	if err != nil {
		log.Fatalf("Unable to connect to broker: %v", err)
	}

	// Loops forever sending any local clipboard changes to broker.
	clientloop(broker, *cfg.topic, polltime, cli, chromequirk)

	// This should never happen.
	return nil
}

// insertConfigFile checks for the existence of a configuration file and
// inserts it as @file before the command line arguments. This causes kingpin
// to read the contents of this file as arguments.
func insertConfigFile(args []string, configFile string) []string {
	if _, err := os.Stat(configFile); err != nil {
		return args
	}
	log.Debugf("Using %q as config file", configFile)
	return append([]string{"@" + configFile}, args...)
}

// tildeExpand expands the tilde at the beginning of a filename to $HOME.
func tildeExpand(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	dirname, err := os.UserHomeDir()
	if err != nil {
		log.Errorf("Unable to locate homedir when expanding: %q", path)
		return path
	}
	return filepath.Join(dirname, path[2:])
}

// configLogging configures the logging parameters from the command line
// options and other conditions.
func configLogging(cfg globalConfig) {
	logFormat := &log.TextFormatter{
		FullTimestamp:          true,
		DisableLevelTruncation: true,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			_, filename := filepath.Split(f.File)
			return fmt.Sprintf("%s()", f.Function), fmt.Sprintf("%s:%d", filename, f.Line)
		},
	}
	// If stdout does not point to a tty, assume we're using syslog/journald
	// and remove the timestamp, since those systems already add it.
	if fi, _ := os.Stdout.Stat(); (fi.Mode() & os.ModeCharDevice) == 0 {
		logFormat.DisableTimestamp = true
	}

	if *cfg.verbose {
		log.SetLevel(log.DebugLevel)
		if *cfg.debug {
			log.SetReportCaller(true)
		}
	}

	if *cfg.nocolors {
		logFormat.DisableColors = true
	}
	log.SetFormatter(logFormat)
}

func main() {
	// General flags
	app := kingpin.New("clipsync", "Sync clipboard across machines")

	cfg := globalConfig{
		cafile:       app.Flag("cafile", "CA certificates file").String(),
		debug:        app.Flag("debug", "Make verbose more verbose").Short('D').Bool(),
		encpassfile:  app.Flag("cryptpass-file", "Encryption password file").String(),
		mqttdebug:    app.Flag("mqtt-debug", "Turn on MQTT debugging").Bool(),
		nocolors:     app.Flag("no-colors", "No colors on log output to terminal.").Bool(),
		password:     app.Flag("password", "MQTT password").Short('p').String(),
		passwordfile: app.Flag("password-file", "File containing the MQTT password").String(),
		redactlevel:  app.Flag("redact-level", "Max number of characters to show on redacted messages").Int(),
		server:       app.Flag("server", "MQTT broker URL. E.g. ssl://ip:port.").Short('s').Required().String(),
		topic:        app.Flag("topic", "MQTT topic").Short('t').Default("clipsync").String(),
		user:         app.Flag("user", "MQTT user").Short('u').String(),
		verbose:      app.Flag("verbose", "Verbose mode.").Short('v').Bool(),
	}

	// Client
	clientCmd := app.Command("client", "Connect to a server and sync clipboards.")
	clientCmdChromeQuirk := clientCmd.Flag("fix-chrome-quirk", "Protect clipboard against one-character copies.").Bool()
	clientCmdSyncSel := clientCmd.Flag("sync-selections", "Synchonize primary (middle mouse) and clipboard (Ctrl-C/V).").Short('S').Bool()
	clientPollTime := app.Flag("poll-time", "Time between clipboard reads (in seconds)").Short('P').Default("1").Int()

	// Copy
	copyCmd := app.Command("copy", "Send contents of stdin to all clipboards.")
	copyCmdFilter := copyCmd.Flag("filter", "Work as a filter: also copy stdin to stdout.").Short('f').Bool()

	// Paste
	pasteCmd := app.Command("paste", "Paste from the server clipboard.")

	// Version
	versionCmd := app.Command("version", "Show version information.")

	// Command-line parsing.
	args := insertConfigFile(os.Args[1:], tildeExpand(configFile))
	cmdline := kingpin.MustParse(app.Parse(args))

	configLogging(cfg)

	// Read password from file, if requested.
	if *cfg.passwordfile != "" {
		p, err := os.ReadFile(tildeExpand(*cfg.passwordfile))
		if err != nil {
			log.Fatal(err)
		}
		*cfg.password = strings.TrimRight(string(p), "\n")
	}

	// Encryption password.
	var cryptPassword []byte
	if *cfg.encpassfile != "" {
		p, err := os.ReadFile(tildeExpand(*cfg.encpassfile))
		if err != nil {
			log.Fatal(err)
		}
		cryptPassword = []byte(strings.TrimRight(string(p), "\n"))
	}

	// Initialize redact object.
	redact = redactType{*cfg.redactlevel}

	// MQTT debugging
	if *cfg.mqttdebug {
		mqtt.DEBUG = log.New()
		mqtt.ERROR = log.New()
		mqtt.CRITICAL = log.New()
		mqtt.WARN = log.New()
	}

	switch cmdline {
	case pasteCmd.FullCommand():
		if err := pastecmd(cfg, cryptPassword); err != nil {
			log.Fatal(err)
		}

	case copyCmd.FullCommand():
		if err := copycmd(cfg, cryptPassword, *copyCmdFilter); err != nil {
			log.Fatal(err)
		}

	case clientCmd.FullCommand():
		// Single instance of client.
		lock := singleInstanceOrDie(syncerLockFile)
		defer lock.Unlock()

		if err := clientcmd(cfg, cryptPassword, *clientPollTime, *clientCmdChromeQuirk, *clientCmdSyncSel); err != nil {
			log.Fatal(err)
		}

	case versionCmd.FullCommand():
		fmt.Printf("Build Version: %s\n", BuildVersion)
	}
	os.Exit(0)
}
