// clipshare - clipboard sharing in go.
package main

import (
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"

	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	// Name of the unix socket file to use.
	sockFile = "/tmp/.clipshare.sock"

	// bufSize for socket reads.
	bufSize = 4096
)

// BuildVersion Holds the current git HEAD version number.
// This is filled in by the build process (make).
var BuildVersion string

func main() {
	var (
		app         = kingpin.New("clipshare", "Clipboard sharing across machines.")
		optNocolors = app.Flag("no-colors", "Verbose mode.").Bool()
		optVerbose  = app.Flag("verbose", "Verbose mode.").Short('v').Bool()

		printCmd   = app.Command("print", "Print the server clipboard.")
		serverCmd  = app.Command("server", "Run in server mode.")
		syncCmd    = app.Command("sync", "Connect to a server and sync clipboards.")
		sendCmd    = app.Command("send", "Send contents of stdin to all clipboards.")
		versionCmd = app.Command("version", "Show version information.")

		err error
	)

	// Command-line parsing.
	k := kingpin.MustParse(app.Parse(os.Args[1:]))

	if *optVerbose {
		log.SetLevel(log.DebugLevel)
	}
	if *optNocolors {
		log.SetFormatter(&log.TextFormatter{
			DisableColors: true,
		})
	}

	switch k {
	case printCmd.FullCommand():
		contents, err := printServerClipboard()
		if err != nil {
			log.Fatalf("Error requesting server clipboard: %v", err)
		}
		fmt.Print(contents)
	case sendCmd.FullCommand():
		if err = publishReader(os.Stdin); err != nil {
			log.Fatalf("Error sending contents to clipboards: %v", err)
		}

	case serverCmd.FullCommand():
		log.Fatalf("Server terminated abnormally: %v\n", server())

	case syncCmd.FullCommand():
		log.Infof("Starting syncer.")
		syncer()

	case versionCmd.FullCommand():
		fmt.Printf("Build Version: %s\n", BuildVersion)
	}
	os.Exit(0)
}
