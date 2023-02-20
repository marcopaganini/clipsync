// This file is part of clipsync (C)2023 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kingpin/v2"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/romana/rlog"
)

const (
	// Config file.
	configDir         = "~/.config/clipsync"
	configFile        = "config"
	cryptPasswordFile = "crypt-password"
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
	cryptfile    *string
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

// randomTopic generates a random topic name based on the SHA256 of the cryptPassword.
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

// readCryptPassword will read the crypt password file and return it.
func readCryptPassword(fname string) ([]byte, error) {
	var pass []byte

	p, err := os.ReadFile(tildeExpand(fname))
	if err != nil {
		return nil, err
	}
	pass = []byte(strings.TrimRight(string(p), "\n"))
	if len(pass) != cryptKeyLen {
		return nil, errors.New("crypt password must be exactly 32 characters")
	}

	return pass, nil
}

// initConfig creates the basic configuration directories under home and generates
// a new crypt-password file with a random password in our default location if
// cryptfile is blank. Returns the name of the cryptfile used (or created). This
// will be the input cryptfile or the default location if cryptfile = blank.
func initConfig(d, f string) (string, error) {
	dir := tildeExpand(d)
	cryptfile := tildeExpand(f)

	// Create the config directory recursively.
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	// We assume this was passed on the command-line and users know what they're doing (ha!)
	if cryptfile != "" {
		return cryptfile, nil
	}

	// Default location.
	cryptfile = filepath.Join(tildeExpand(configDir), tildeExpand(cryptPasswordFile))
	log.Infof("Using crypt file: %s", cryptfile)

	// Create a brand new crypt file if it does not exist.
	if !fileExists(cryptfile) {
		p := createPassword()
		if err := os.WriteFile(cryptfile, p, 0700); err != nil {
			return "", err
		}
		log.Infof("Created a new crypt file with a random password at %s", cryptfile)
	}

	return cryptfile, nil
}

func main() {
	var err error

	// General flags
	app := kingpin.New("clipsync", "Sync clipboard across machines")

	cfg := globalConfig{
		cafile:       app.Flag("cafile", "CA certificates file (usually /etc/ssl/certs/ca-certificates.crt").String(),
		debug:        app.Flag("debug", "Make verbose more verbose").Short('D').Bool(),
		cryptfile:    app.Flag("crypt-file", "File containing a 32-byte clipboard encryption password").String(),
		mqttdebug:    app.Flag("mqtt-debug", "Turn on MQTT debugging").Bool(),
		nocolors:     app.Flag("no-colors", "No colors on log output to terminal.").Bool(),
		password:     app.Flag("password", "MQTT password").Short('p').String(),
		passwordfile: app.Flag("password-file", "File containing the MQTT password").String(),
		randomtopic:  app.Flag("random-topic", "Use a random topic name based on your encryption key.").Bool(),
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
	args := insertConfigFile(os.Args[1:], tildeExpand(filepath.Join(configDir, configFile)))
	cmdline := kingpin.MustParse(app.Parse(args))

	setupLogging(cfg)

	// Create basic directories and a crypt file containing a
	// random key, if it doesn't yet exist and is in the default
	// location (blank).
	*cfg.cryptfile, err = initConfig(configDir, *cfg.cryptfile)
	if err != nil {
		fatalf("Error initializing configuration: %v", err)
	}

	// Read MQTT password from file, if requested.
	if *cfg.passwordfile != "" {
		p, err := os.ReadFile(tildeExpand(*cfg.passwordfile))
		if err != nil {
			fatalf("Error reading password file: %v", err)
		}
		*cfg.password = strings.TrimRight(string(p), "\n")
	}

	cryptPassword, err := readCryptPassword(*cfg.cryptfile)
	if err != nil {
		fatalf("Error reading crypt password: %v", err)
	}

	// Read CA File into our filesystem, if requested.
	if *cfg.cafile != "" {
		cfg.cert, err = os.ReadFile(*cfg.cafile)
		if err != nil {
			fatalf("Unable to read CA file: %v", err)
		}
	}

	// Initialize redact object.
	redact = redactType{*cfg.redactlevel}

	// MQTT debugging
	if *cfg.mqttdebug {
		mqttlog := rlogger{}
		mqtt.DEBUG = mqttlog
		mqtt.ERROR = mqttlog
		mqtt.CRITICAL = mqttlog
		mqtt.WARN = mqttlog
	}

	// If no server was specified, we assume a connection to a public server
	// (test.mosquitto.org).  There's a number of parameters that we need to
	// override.
	if *cfg.server == "" {
		log.Info("No server specified. Using public server. YMMV.")
		*cfg.user = ""
		*cfg.password = ""
		*cfg.randomtopic = true
		*cfg.server = "test.mosquitto.org:1883"
	}

	// if randomtopic uses a random topic based on the sha256 of the cryptPassword.
	if *cfg.randomtopic {
		*cfg.topic = randomTopic(cryptPassword)
	}

	// Make sure host is not blank after any possible overrides.
	if *cfg.server == "" {
		fatal("I don't have a server right before starting to work. This should not happen.")
	}

	switch cmdline {
	case pasteCmd.FullCommand():
		if err := pastecmd(cfg, cryptPassword); err != nil {
			fatal(err)
		}

	case copyCmd.FullCommand():
		if err := copycmd(cfg, cryptPassword, *copyCmdFilter); err != nil {
			fatal(err)
		}

	case clientCmd.FullCommand():
		// Single instance of client.
		lock := singleInstanceOrDie(syncerLockFile)
		defer lock.Unlock()

		if err := clientcmd(cfg, clientcfg, cryptPassword); err != nil {
			fatal(err)
		}

	case versionCmd.FullCommand():
		fmt.Printf("Build Version: %s\n", BuildVersion)
	}
	os.Exit(0)
}
