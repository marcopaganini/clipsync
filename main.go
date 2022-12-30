// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"

	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	// Config file.
	configFile = "~/.config/clipsync/config"
)

// BuildVersion Holds the current git HEAD version number.
// This is filled in by the build process (make).
var BuildVersion string

// globalConfig holds the user global configurations as requested in the
// command line or in the configuration file.
type globalConfig struct {
	cafile       *string
	cert         []byte
	debug        *bool
	encpassfile  *string
	mqttdebug    *bool
	nocolors     *bool
	password     *string
	passwordfile *string
	randomtopic  *bool
	redactlevel  *int
	server       *string
	topic        *string
	user         *string
	verbose      *bool
}

// clientConfig holds the options for the "client" operation.
type clientConfig struct {
	chromequirk *bool
	syncsel     *bool
	polltime    *int
}

// The redact object is used by other functions in this namespace.
var redact redactType

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

// setupLogging configures the logging parameters from the command line
// options and other conditions.
func setupLogging(cfg globalConfig) {
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

// randomTopic generates a random topic name based on the SHA256 of the cryptoPassword.
// The topic is broken down into multiple sections to avoid very long names.
func randomTopic(pass []byte) string {
	hasher := sha256.New()
	hasher.Write(pass)
	hexascii := hex.EncodeToString(hasher.Sum(nil))

	// Break down sha256 hash into 4 groups of 16 characters each, separated by slashes.
	var frags []string
	for start := 0; start < 64; start += 16 {
		frags = append(frags, hexascii[start:start+16])
	}
	return strings.Join(frags, "/")
}

func main() {
	var err error

	// General flags
	app := kingpin.New("clipsync", "Sync clipboard across machines")

	cfg := globalConfig{
		cafile:       app.Flag("cafile", "CA certificates file (usually /etc/ssl/certs/ca-certificates.crt").String(),
		debug:        app.Flag("debug", "Make verbose more verbose").Short('D').Bool(),
		encpassfile:  app.Flag("cryptpass-file", "Encryption password file").String(),
		mqttdebug:    app.Flag("mqtt-debug", "Turn on MQTT debugging").Bool(),
		nocolors:     app.Flag("no-colors", "No colors on log output to terminal.").Bool(),
		password:     app.Flag("password", "MQTT password").Short('p').String(),
		passwordfile: app.Flag("password-file", "File containing the MQTT password").String(),
		randomtopic:  app.Flag("random-topic", "Generate a random topic based on your encryption key.").Bool(),
		redactlevel:  app.Flag("redact-level", "Max number of characters to show on redacted messages").Int(),
		server:       app.Flag("server", "MQTT broker URL. E.g. ssl://ip:port.").Short('s').String(),
		topic:        app.Flag("topic", "MQTT topic").Short('t').Default("clipsync").String(),
		user:         app.Flag("user", "MQTT user").Short('u').String(),
		verbose:      app.Flag("verbose", "Verbose mode.").Short('v').Bool(),
	}

	// Client
	clientCmd := app.Command("client", "Connect to a server and sync clipboards.")
	clientcfg := clientConfig{
		chromequirk: clientCmd.Flag("fix-chrome-quirk", "Protect clipboard against one-character copies.").Bool(),
		syncsel:     clientCmd.Flag("sync-selections", "Synchonize primary (middle mouse) and clipboard (Ctrl-C/V).").Short('S').Bool(),
		polltime:    app.Flag("poll-time", "Time between clipboard reads (in seconds)").Short('P').Default("1").Int(),
	}

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

	setupLogging(cfg)

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

	// Read CA File into our filesystem, if requested.
	if *cfg.cafile != "" {
		cfg.cert, err = os.ReadFile(*cfg.cafile)
		if err != nil {
			log.Fatalf("Unable to read CA file: %v", err)
		}
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

	// If no server was specified, we assume a connection to a public server
	// (test.mosquitto.org).  There's a number of parameters that need to be
	// overriden.
	if *cfg.server == "" {
		if len(cryptPassword) == 0 {
			log.Fatal("Must specify an encrypted pasword file (--cryptpass-file) with public servers.")
		}
		*cfg.user = ""
		*cfg.password = ""
		*cfg.randomtopic = true
		*cfg.server = "test.mosquitto.org:1883"
	}

	// if randomtopic was chosen, generate a random topic based on the sha256 of the cryptPassword.
	if *cfg.randomtopic {
		if len(cryptPassword) == 0 {
			log.Fatal("Must specify an encrypted pasword file (--encpassfile) when using random topics .")
		}
		*cfg.topic = randomTopic(cryptPassword)
		log.Infof("Using random topic: %s\n", *cfg.topic)
	}

	// Make sure host is not blank after any possible overrides.
	if *cfg.server == "" {
		log.Fatal("Must specify a server with the --server=address:port command-line flag.")
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

		if err := clientcmd(cfg, clientcfg, cryptPassword); err != nil {
			log.Fatal(err)
		}

	case versionCmd.FullCommand():
		fmt.Printf("Build Version: %s\n", BuildVersion)
	}
	os.Exit(0)
}
