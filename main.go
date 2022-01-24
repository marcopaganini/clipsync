// clipshare - clipboard sharing in go.
package main

import (
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"

	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	sockFilename = ".clipshare.sock"

	// bufSize for socket reads.
	bufSize = 32 * 1024 * 1024
)

// BuildVersion Holds the current git HEAD version number.
// This is filled in by the build process (make).
var BuildVersion string

func main() {
	var (
		app         = kingpin.New("clipshare", "Clipboard sharing across machines.")
		optNocolors = app.Flag("no-colors", "Verbose mode.").Bool()
		optVerbose  = app.Flag("verbose", "Verbose mode.").Short('v').Bool()

		copyCmd       = app.Command("copy", "Send contents of stdin to all clipboards.")
		copyCmdFilter = copyCmd.Flag("filter", "Work as a filter: also copy stdin to stdout.").Short('f').Bool()

		pasteCmd = app.Command("paste", "Paste from the server clipboard.")

		serverCmd = app.Command("server", "Run in server mode.")

		syncCmd        = app.Command("sync", "Connect to a server and sync clipboards.")
		syncCmdProtect = syncCmd.Flag("no-single-char", "Protect clipboard against one-character copies.").Short('p').Bool()
		syncCmdBoth    = syncCmd.Flag("both", "Synchonize primary (middle mouse) and clipboard (Ctrl-C/V).").Short('2').Bool()

		versionCmd = app.Command("version", "Show version information.")

		err error
	)

	// Command-line parsing.
	k := kingpin.MustParse(app.Parse(os.Args[1:]))

	// Log formatting options.
	if *optVerbose {
		log.SetLevel(log.DebugLevel)
	}
	logFormat := &log.TextFormatter{
		FullTimestamp:          true,
		DisableLevelTruncation: true,
	}
	if *optNocolors {
		logFormat.DisableColors = true
	}
	log.SetFormatter(logFormat)

	// Used by multiple actions.
	sockfile, err := sockPath(sockFilename)
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
		if err = publishReader(sockfile, os.Stdin, *copyCmdFilter); err != nil {
			log.Fatalf("Error sending contents to clipboards: %v", err)
		}

	case serverCmd.FullCommand():
		log.Fatalf("Server terminated abnormally: %v\n", server(sockfile))

	case syncCmd.FullCommand():
		log.Infof("Starting syncer.")
		syncer(sockfile, *syncCmdProtect, *syncCmdBoth)

	case versionCmd.FullCommand():
		fmt.Printf("Build Version: %s\n", BuildVersion)
	}
	os.Exit(0)
}
