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
	"io/ioutil"
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

// BuildVersion Holds the current git HEAD version number.
// This is filled in by the build process (make).
var BuildVersion string

// The redact object is used by other functions in this namespace.
var redact redactType

func newBroker(server, topic, user, password, cafile string, handler func(client mqtt.Client, msg mqtt.Message)) (mqtt.Client, error) {
	tlsconfig, err := newTLSConfig(cafile)
	if err != nil {
		return nil, err
	}
	opts := mqtt.NewClientOptions()
	opts.AddBroker(server)

	// Client ID must be unique.
	id := uuid.New()
	clientID := "clipsync-" + id.String()
	opts.SetClientID(clientID)
	log.Debugf("Set MQTT Client ID to %v", clientID)

	opts.SetKeepAlive(4 * time.Second)
	opts.SetTLSConfig(tlsconfig)
	opts.SetPingTimeout(2 * time.Second)
	opts.SetAutoReconnect(true)

	if user != "" {
		opts.SetUsername(user)
	}
	if password != "" {
		opts.SetPassword(password)
	}

	// If handler is present, assume we'll subscribe to a topic. In this case,
	// set OnConnectHandler to re-subscribe every time we have a connection.
	// This, together with SetAutoReconnect guarantees that we'll keep
	// receiving messages from the topic after an automatic reconnect.
	if handler != nil {
		opts.SetOnConnectHandler(func(onconn mqtt.Client) {
			log.Debugf("Connection detected. Subscribing to topic: %q", topic)
			if token := onconn.Subscribe(topic, 0, handler); token.Wait() && token.Error() != nil {
				log.Errorf("Unable to subscribe to topic %s: %v", topic, token.Error())
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
		pemCerts, err := ioutil.ReadFile(cafile)
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
func pastecmd(server, topic, user, password, cafile string) error {
	log.Debug("Got paste command")
	ch := make(chan string)
	broker, err := newBroker(server, topic, user, password, cafile, func(client mqtt.Client, msg mqtt.Message) {
		data := string(msg.Payload())
		log.Debugf("Received from server: %s", redact.redact(data))
		ch <- data
		return
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
func copycmd(server, topic, user, password, cafile string, filter bool) error {
	log.Debug("Got copy command")
	broker, err := newBroker(server, topic, user, password, cafile, nil)
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
	if token := broker.Publish(topic, 0, true, spub); token.Wait() && token.Error() != nil {
		return fmt.Errorf("Error publishing data: %v", token.Error())
	}
	if filter {
		fmt.Print(string(pub))
	}

	return nil
}

// clientcmd activates "client" mode, syncing the local clipboard to the server
// and vice-versa. This function will only return in case of error.
func clientcmd(server, topic, user, password, cafile string, polltime int, chromequirk, syncsel bool) error {
	// Client mode only makes sense if the DISPLAY environment
	// variable is set (otherwise we don't have a clipboard to sync).
	if os.Getenv("DISPLAY") == "" {
		return fmt.Errorf("Client mode requires the DISPLAY variable to be set")
	}

	log.Infof("Starting client, server: %s", server)
	cli := &client{}
	broker, err := newBroker(server, topic, user, password, cafile, cli.subHandler)
	if err != nil {
		log.Fatalf("Unable to connect to broker: %v", err)
	}
	// Loops forever sending any local clipboard changes to broker.
	clientloop(broker, topic, polltime, cli, chromequirk, syncsel)

	// This should never happen.
	return nil
}

func main() {
	var (
		// General flags
		app             = kingpin.New("clipsync", "Sync clipboard across machines")
		optCAFile       = app.Flag("cafile", "CA certificates file").String()
		optDebug        = app.Flag("debug", "Make verbose more verbose").Short('D').Bool()
		optLogFile      = app.Flag("logfile", "Log file (stderr if not specified)").Short('L').String()
		optMQTTDebug    = app.Flag("mqtt-debug", "Turn on MQTT debugging").Bool()
		optNocolors     = app.Flag("no-colors", "No colors on log output to terminal.").Bool()
		optPassword     = app.Flag("password", "MQTT password").Short('p').String()
		optPasswordFile = app.Flag("password-file", "File containing the MQTT password").String()
		optRedactLevel  = app.Flag("redact-level", "Max number of characters to show on redacted messages").Int()
		optServer       = app.Flag("server", "MQTT broker URL. E.g. ssl://ip:port.").Short('s').Required().String()
		optTopic        = app.Flag("topic", "MQTT topic").Short('t').Default("clipsync").String()
		optUser         = app.Flag("user", "MQTT user").Short('u').String()
		optVerbose      = app.Flag("verbose", "Verbose mode.").Short('v').Bool()

		// Client
		clientCmd            = app.Command("client", "Connect to a server and sync clipboards.")
		clientCmdChromeQuirk = clientCmd.Flag("fix-chrome-quirk", "Protect clipboard against one-character copies.").Bool()
		clientCmdSyncSel     = clientCmd.Flag("sync-selections", "Synchonize primary (middle mouse) and clipboard (Ctrl-C/V).").Short('S').Bool()
		clientPollTime       = app.Flag("poll-time", "Time between clipboard reads (in seconds)").Short('P').Default("1").Int()

		// Copy
		copyCmd       = app.Command("copy", "Send contents of stdin to all clipboards.")
		copyCmdFilter = copyCmd.Flag("filter", "Work as a filter: also copy stdin to stdout.").Short('f').Bool()

		// Paste
		pasteCmd = app.Command("paste", "Paste from the server clipboard.")

		// Version
		versionCmd = app.Command("version", "Show version information.")
	)

	// Command-line parsing.
	cmdline := kingpin.MustParse(app.Parse(os.Args[1:]))

	// Logfile.
	if *optLogFile != "" {
		logf, err := os.OpenFile(*optLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		defer logf.Close()
		log.SetOutput(logf)
	}

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

	if *optVerbose {
		log.SetLevel(log.DebugLevel)
		if *optDebug {
			log.SetReportCaller(true)
		}
	}

	if *optNocolors {
		logFormat.DisableColors = true
	}
	log.SetFormatter(logFormat)

	// Password.
	password := *optPassword
	if *optPasswordFile != "" {
		p, err := ioutil.ReadFile(*optPasswordFile)
		if err != nil {
			log.Fatal(err)
		}
		password = strings.TrimRight(string(p), "\n")
	}

	// Initialize redact object.
	redact = redactType{*optRedactLevel}

	// MQTT debugging
	if *optMQTTDebug {
		mqtt.DEBUG = log.New()
		mqtt.ERROR = log.New()
		mqtt.CRITICAL = log.New()
		mqtt.WARN = log.New()
	}

	switch cmdline {
	case pasteCmd.FullCommand():
		if err := pastecmd(*optServer, *optTopic, *optUser, password, *optCAFile); err != nil {
			log.Fatal(err)
		}

	case copyCmd.FullCommand():
		if err := copycmd(*optServer, *optTopic, *optUser, password, *optCAFile, *copyCmdFilter); err != nil {
			log.Fatal(err)
		}

	case clientCmd.FullCommand():
		// Single instance of client.
		lock := singleInstanceOrDie(syncerLockFile)
		defer lock.Unlock()

		if err := clientcmd(*optServer, *optTopic, *optUser, password, *optCAFile, *clientPollTime, *clientCmdChromeQuirk, *clientCmdSyncSel); err != nil {
			log.Fatal(err)
		}

	case versionCmd.FullCommand():
		fmt.Printf("Build Version: %s\n", BuildVersion)
	}
	os.Exit(0)
}
