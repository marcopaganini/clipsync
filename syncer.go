package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"

	backoff "github.com/cenkalti/backoff/v4"
	"github.com/fredli74/lockfile"
	log "github.com/sirupsen/logrus"
)

const (
	syncerLockFile = "/var/run/lock/clipshare-syncer.lock"
)

// publishToServer opens a socket to the server and publishes the contents.
func publishToServer(contents string) error {
	sockfile, err := sockPath(sockFilename)
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", sockfile)
	if err != nil {
		log.Errorf("publishToServer: %v", err)
		return err
	}
	if _, err := fmt.Fprintf(conn, "PUB\n%s", contents); err != nil {
		log.Errorf("publishToServer: Error writing to socket: %v", err)
	}
	conn.Close()
	log.Debugf("publishToServer: sent %q", contents)
	return nil
}

// subscribeToServer constantly reads from the server and updates the in-memory
// clipboard, and the local (if DISPLAY is set) with any changes reported by
// the remote.
func subscribeToServer(sockfile string, clip *clipboard) {
	for {
		// Create connection.
		buf := make([]byte, bufSize)

		// Dial and send subscribe command (with exponential backoff).
		var conn net.Conn

		backoff.Retry(func() error {
			var err error

			log.Infof("Creating connection to server.")

			conn, err = net.Dial("unix", sockfile)
			if err != nil {
				log.Errorf("subcribeToServer: %v", err)
				return err
			}
			log.Infof("subcribeToServer: Connected to %s", sockfile)

			// Send Subscribe command.
			if _, err = fmt.Fprintln(conn, "SUB"); err != nil {
				log.Infof("subscribeToServer: Error writing to socket: %v\n", err)
				return err
			}
			return nil
		}, backoff.NewExponentialBackOff())

		// Read contents until killed.
		for {
			nbytes, err := conn.Read(buf[:])
			if err != nil {
				log.Errorf("subcribeToServer: Error reading socket: %v", err)
				break
			}
			data := string(buf[0:nbytes])
			value := clip.get()
			log.Debugf("subscribeToServer: Received %q, current memory clipboard: %q", data, value)
			if data != value {
				clip.set(data)
				if os.Getenv("DISPLAY") != "" {
					if err = writeClipboard(data); err != nil {
						log.Errorf("subcribeToServer: Unable to set local clipboard: %v", err)
					}
				}
			}
		}
		log.Debugf("subscribeToServer: Closing connection")
		conn.Close()
		time.Sleep(3 * time.Second)
	}
}

// publishClipboard periodically reads from this machine's clipboard and
// updates the remote clipboard server when changes happen. This function
// never returns.
func publishClipboard(clip *clipboard) {
	log.Debugf("About to publishClipboard")
	for {
		xclipboard := readClipboard()
		value := clip.get()

		// No changes, move on...
		if value == xclipboard {
			time.Sleep(time.Second)
			continue
		}

		// Set in-memory clipboard and publish to server.
		clip.set(xclipboard)
		log.Debugf("publishClipboard: Got remote clipboard value: %s", xclipboard)
		if err := publishToServer(xclipboard); err != nil {
			log.Errorf("publishClipboard: Failed to set remote clipboard: %v", err)
			time.Sleep(time.Second)
			continue
		}

		time.Sleep(time.Second)
	}
}

// publishReader sends the contents of the io.Reader to all clipboards. The
// local clipboard will be set by the syncer (running in another instance). If
// 'filter' is set, the contents of the standard input are re-printed in the
// standard output.
func publishReader(r io.Reader, filter bool) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	contents := string(data)

	if filter {
		fmt.Print(contents)
	}
	if err = publishToServer(contents); err != nil {
		return err
	}
	return nil
}

// syncer maintains the local clipboard synchronized with the remote server
// clipboard. Subscribing to a server will sync the in-memory version of the
// clipboard to that server.
func syncer() {
	// Allow only one instance.
	if lock, err := lockfile.Lock(syncerLockFile); err != nil {
		log.Fatalf("Another instance of the syncer is already running.")
	} else {
		defer lock.Unlock()
	}

	sockfile, err := sockPath(sockFilename)
	if err != nil {
		log.Fatal(err)
	}
	clip := &clipboard{}
	go subscribeToServer(sockfile, clip)

	// Only attempt to sync the local (machine) clipboard if the DISPLAY
	// environment variable is set.
	if os.Getenv("DISPLAY") != "" {
		// Runs forever.
		publishClipboard(clip)
	}

	// No DISPLAY, sleep forever
	log.Debugf("syncer: DISPLAY variable is not set. Won't set X's clipboard. Sleeping forever.")
	for {
		time.Sleep(1e9 * time.Second)
	}
}
