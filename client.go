// client.go - Client functions for clipsync.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	backoff "github.com/cenkalti/backoff/v4"
	log "github.com/sirupsen/logrus"
)

const (
	syncerLockFile = "/var/run/lock/clipsync-client.lock"
)

// publishToServer opens a socket to the server and publishes the contents.
func publishToServer(sockfile string, contents string) error {
	conn, err := net.Dial("unix", sockfile)
	if err != nil {
		log.Errorf("publishToServer: %v", err)
		return err
	}
	log.Debugf("Publishing to server: %s\n", redact(contents))
	if _, err := conn.Write([]byte("PUB\n" + contents + "\x00")); err != nil {
		log.Errorf("publishToServer: Error writing to socket: %v", err)
	}
	conn.Close()
	return nil
}

// subscribeToServer constantly reads from the server and updates the local
// selection with any changes reported by the remote.
func subscribeToServer(sockfile string, sel *selection) {
	for {
		// Dial and send subscribe command (with exponential backoff).
		var conn net.Conn

		backoff.Retry(func() error {
			var err error

			log.Infof("Creating connection to server.")

			conn, err = net.Dial("unix", sockfile)
			if err != nil {
				log.Error(err)
				return err
			}
			log.Infof("Connected to %s", sockfile)

			// Send Subscribe command.
			if _, err = fmt.Fprint(conn, "SUB\n\x00"); err != nil {
				log.Infof("Error writing to socket: %v\n", err)
				return err
			}
			return nil
		}, backoff.NewExponentialBackOff())

		// Read contents until killed.
		for {
			buf, err := bufio.NewReader(conn).ReadBytes('\x00')
			if err != nil {
				log.Errorf("Error reading socket: %v", err)
				break
			}
			data := strings.TrimSuffix(string(buf), "\x00")
			value := sel.getPrimary()
			log.Debugf("Received from server: %s", redact(data))
			log.Debugf("Current memory primary selection: %s", redact(value))
			if data != value {
				log.Debugf("Values differ. Will write to primary selection")
				// Don't set the memory clipboard here, just the selection.
				// This will cause publishSelection to automatically sync the
				// primary selection to the clipboard, if required.
				if err = setXPrimary(data); err != nil {
					log.Errorf("Unable to set local primary selection: %v", err)
				}
			}
		}
		log.Debugf("Closing connection.")
		conn.Close()
		time.Sleep(3 * time.Second)
	}
}

// publishSelection periodically reads from this machine's primary selection
// and updates the remote clipboard server when changes happen. This function
// never returns.
//
// If chromeQuirk is set, the function restores the primary selection when it
// contains a single rune (character or UTF character). This is a workaround for
// Chrome in Linux where chrome sometimes overwrites the primary selection with
// a single character when compose sequences are used.
//
// if syncSelections is set, keep both primary and clipboard selections in
// sync (i.e. setting one will also set the other). Note that the server
// only handles one version of the clipboard.
//
// Note: For now, reading and writing to the clipboard is somewhat of an
// expensive operation as it requires calling xclip. This will be changed in a
// future version, which should allow us to simplify this function.
func publishSelection(sockfile string, pollTime int, sel *selection, chromeQuirk bool, syncSelections bool) {
	for {
		time.Sleep(time.Duration(pollTime) * time.Second)

		xprimary := getXPrimary("")

		// Restore the primary selection to the saved value if it contains
		// a single rune and 'protect' is set.
		memPrimary := sel.getPrimary()
		if chromeQuirk && utf8.RuneCountInString(xprimary) == 1 {
			xprimary = memPrimary
			if err := setXPrimary(memPrimary); err != nil {
				log.Errorf("Cannot write to primary selection: %v", err)
			}
		}

		// Sync primary and clipboard, if requested. This will change the
		// selections (sel) if sync is needed.
		if syncSelections {
			if err := syncPrimaryAndClip(sockfile, xprimary, getXClipboard("text/plain"), sel); err != nil {
				log.Errorf("Error syncing selections (primary/clipboard): %v", err)
			}
			continue
		}

		// Only publish if our original clipboard has changed.
		if sel.getPrimary() != xprimary {
			// Set in-memory primary selection and publish to server.
			sel.setPrimary(xprimary)
			log.Debugf("Got remote clipboard value: %s", redact(xprimary))
			if err := publishToServer(sockfile, xprimary); err != nil {
				log.Errorf("Failed to set remote clipboard: %v", err)
			}
		}
	}
}

// publishPrimary publishes the given string to the server.
//
// syncPrimaryAndClip synchronizes the primary selection to the clipboard (and vice-versa).
func syncPrimaryAndClip(sockfile, xprimary, xclipboard string, sel *selection) error {
	var publish string

	// X clipboard changed? Sync to memory and X primary selection.
	// Ignore blank returns as they could be an error in xclip or no
	// content in the clipboard with the desired mime-type.
	if xclipboard != "" && xclipboard != sel.getClipboard() {
		sel.setPrimary(xclipboard)
		sel.setClipboard(xclipboard)
		publish = xclipboard
		if err := setXPrimary(xclipboard); err != nil {
			return err
		}
	}

	// X primary changed? Sync to memory and X clipboard.
	if xprimary != "" && xprimary != sel.getPrimary() {
		// primary changed, sync to clipboard.
		sel.setPrimary(xprimary)
		sel.setClipboard(xprimary)
		publish = xprimary
		if err := setXClipboard(xprimary); err != nil {
			return err
		}
	}

	// Publish to server, if needed
	if publish != "" {
		if err := publishToServer(sockfile, publish); err != nil {
			return err
		}
	}

	return nil
}

// publishReader sends the contents of the io.Reader to all clipboards. The
// local primary selection will be set by the syncer (running in another
// instance). If 'filter' is set, the contents of the standard input are
// re-printed to the standard output.
func publishReader(sockfile string, r io.Reader, filter bool) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	contents := string(data)

	// Publish to server and continue, even on error. Printing input to stdout
	// (in case --filter has been requested has to be done AFTER publishing.
	// This guarantees that if the program is killed by SIGPIPE, we'll already
	// have sent the information to the clipsync server.
	err = publishToServer(sockfile, contents)

	if filter {
		fmt.Print(contents)
	}
	return err
}

// client maintains the local primary selection synchronized with the remote
// server clipboard. Subscribing to a server will sync the in-memory version of
// the primary selection to that server.
func client(sockfile string, pollTime int, chromeQuirk bool, syncSelections bool) {
	lock := singleInstanceOrDie(syncerLockFile)
	defer lock.Unlock()

	sel := &selection{}
	go subscribeToServer(sockfile, sel)

	// Runs forever.
	publishSelection(sockfile, pollTime, sel, chromeQuirk, syncSelections)
}
