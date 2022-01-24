package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"
	"unicode/utf8"

	backoff "github.com/cenkalti/backoff/v4"
	log "github.com/sirupsen/logrus"
)

const (
	syncerLockFile = "/var/run/lock/clipshare-syncer.lock"
)

// publishToServer opens a socket to the server and publishes the contents.
func publishToServer(sockfile string, contents string) error {
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
// selection, and the local (if DISPLAY is set) with any changes reported by
// the remote.
func subscribeToServer(sockfile string, sel *selection) {
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
			value := sel.getPrimary()
			log.Debugf("subscribeToServer: Received %q, current memory primary selection: %q", data, value)
			if data != value {
				sel.setPrimary(data)
				if os.Getenv("DISPLAY") != "" {
					if err = writeSelection(data, selPrimary); err != nil {
						log.Errorf("subcribeToServer: Unable to set local primary selection: %v", err)
					}
				}
			}
		}
		log.Debugf("subscribeToServer: Closing connection.")
		conn.Close()
		time.Sleep(3 * time.Second)
	}
}

// publishSelection periodically reads from this machine's primary selection
// and updates the remote clipboard server when changes happen. This function
// never returns.
//
// If 'protect' is set, we activate "single character protection": Basically,
// ignore the clipboard (and restore it from the last good known value) if it
// contains only one character. This is a workaround to a bugs in Chrome Linux
// (chrome overwrites the primary selection with one character when a
// composition sequence is used.)
func publishSelection(sockfile string, sel *selection, protect bool, both bool) {
	for {
		xprimary := readSelection(selPrimary)
		xclipboard := readSelection(selClipboard)

		memPrimary := sel.getPrimary()
		memClipboard := sel.getClipboard()

		// Restore the primary selection to the saved value if it contains
		// a single rune and 'protect' is set.
		if protect && utf8.RuneCountInString(xprimary) == 1 {
			xprimary = memPrimary
			if err := writeSelection(memPrimary, selPrimary); err != nil {
				log.Errorf("publishSelection: Cannot write to primary selection: %v", err)
			}
		}

		// Sync primary and clipboard, if requested.
		if both {
			// clipboard changed, sync to primary.
			if xclipboard != memClipboard {
				xprimary = xclipboard
				sel.setPrimary(xclipboard)
				sel.setClipboard(xclipboard)
				if err := writeSelection(xclipboard, selPrimary); err != nil {
					log.Errorf("publishSelection: Cannot write to primary selection: %v", err)
				}
			} else if xprimary != memPrimary {
				// primary changed, sync to clipboard.
				sel.setClipboard(xprimary)
				if err := writeSelection(xprimary, selClipboard); err != nil {
					log.Errorf("publishSelection: Cannot write to clipboard: %v", err)
				}
			}
		}

		// Don't publish if there are no changes.
		if memPrimary == xprimary {
			time.Sleep(time.Second)
			continue
		}

		// Set in-memory primary selection and publish to server.
		sel.setPrimary(xprimary)
		log.Debugf("publishSelection: Got remote clipboard value: %s", xprimary)
		if err := publishToServer(sockfile, xprimary); err != nil {
			log.Errorf("publishSelection: Failed to set remote clipboard: %v", err)
			time.Sleep(time.Second)
			continue
		}

		time.Sleep(time.Second)
	}
}

// publishReader sends the contents of the io.Reader to all clipboards. The
// local primary selection will be set by the syncer (running in another
// instance). If 'filter' is set, the contents of the standard input are
// re-printed in the standard output.
func publishReader(sockfile string, r io.Reader, filter bool) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	contents := string(data)

	if filter {
		fmt.Print(contents)
	}
	if err = publishToServer(sockfile, contents); err != nil {
		return err
	}
	return nil
}

// syncer maintains the local primary selection synchronized with the remote
// server clipboard. Subscribing to a server will sync the in-memory version of
// the primary selection to that server.
func syncer(sockfile string, protect bool, both bool) {
	lock := singleInstanceOrDie(syncerLockFile)
	defer lock.Unlock()

	sel := &selection{}
	go subscribeToServer(sockfile, sel)

	// Only attempt to sync the local (machine) primary selection if the DISPLAY
	// environment variable is set.
	if os.Getenv("DISPLAY") != "" {
		// Runs forever.
		publishSelection(sockfile, sel, protect, both)
	}

	// No DISPLAY, sleep forever
	log.Debugf("syncer: DISPLAY variable is not set. Won't set X selection. Sleeping forever.")
	for {
		time.Sleep(1e9 * time.Second)
	}
}
