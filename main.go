// clipsync - Synchronize clipboard across machines.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	log "github.com/sirupsen/logrus"

	"gopkg.in/alecthomas/kingpin.v2"
)

// BuildVersion Holds the current git HEAD version number.
// This is filled in by the build process (make).
var BuildVersion string

func main() {
	var (
		// General flags
		app         = kingpin.New("clipsync", "Sync clipboard across machines")
		optNocolors = app.Flag("no-colors", "Don't use colors.").Bool()
		optVerbose  = app.Flag("verbose", "Verbose mode.").Short('v').Bool()
		optSockFile = app.Flag("sockfile", "Local socket file.").Short('S').String()
		optLogFile  = app.Flag("logfile", "Log file (stderr if not specified)").Short('L').String()

		// Client
		clientCmd            = app.Command("client", "Connect to a server and sync clipboards.")
		clientCmdChromeQuirk = clientCmd.Flag("fix-chrome-quirk", "Protect clipboard against one-character copies.").Bool()
		clientCmdSyncSel     = clientCmd.Flag("sync-selections", "Synchonize primary (middle mouse) and clipboard (Ctrl-C/V).").Short('s').Bool()
		clientPollTime       = app.Flag("poll-time", "Time between clipboard reads (in seconds)").Short('p').Default("1").Int()

		// Copy
		copyCmd       = app.Command("copy", "Send contents of stdin to all clipboards.")
		copyCmdFilter = copyCmd.Flag("filter", "Work as a filter: also copy stdin to stdout.").Short('f').Bool()

		// Paste
		pasteCmd = app.Command("paste", "Paste from the server clipboard.")

		// Server
		serverCmd = app.Command("server", "Run in server mode.")

		// Version
		versionCmd = app.Command("version", "Show version information.")

		err error
	)

	// Command-line parsing.
	k := kingpin.MustParse(app.Parse(os.Args[1:]))

	// Log formatting options.
	if *optVerbose {
		log.SetLevel(log.DebugLevel)
		log.SetReportCaller(true)
	}

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
	if *optNocolors {
		logFormat.DisableColors = true
	}
	log.SetFormatter(logFormat)

	// Used by multiple actions.
	sockfile, err := makeSockFile(*optSockFile)
	if err != nil {
		log.Fatalf("Unable to generate socket file name: %v", err)
	}

	switch k {
	case pasteCmd.FullCommand():
		contents, err := printServerClipboard(sockfile)
		if err != nil {
			log.Fatalf("Error requesting server clipboard: %v", err)
		}
		fmt.Print(contents)

	case copyCmd.FullCommand():
		log.Debug("Got copy command")
		if err = publishReader(sockfile, os.Stdin, *copyCmdFilter); err != nil {
			log.Fatalf("Error publishing content to server: %v", err)
		}

	case serverCmd.FullCommand():
		log.Fatalf("Server terminated abnormally: %v", server(sockfile))

	case clientCmd.FullCommand():
		// Client mode only makes sense if the DISPLAY environment
		// variable is set (otherwise we don't have a clipboard to sync).
		if os.Getenv("DISPLAY") == "" {
			fmt.Printf("The DISPLAY environment variable is not set.\n")
			fmt.Printf("This means that we don't have a local clipboard to sync to the server.\n")
			fmt.Printf("Make sure you run this command inside an X session.\n")
			os.Exit(1)
		}
		log.Infof("Starting client.")
		client(sockfile, *clientPollTime, *clientCmdChromeQuirk, *clientCmdSyncSel)

	case versionCmd.FullCommand():
		fmt.Printf("Build Version: %s\n", BuildVersion)
	}
	os.Exit(0)
}
