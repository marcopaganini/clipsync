package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"

	log "github.com/sirupsen/logrus"
)

// readClipboard returns the contents of the primary clipboard.
func readClipboard() (string, error) {
	xclip := exec.Command("xclip", "-o")
	out, err := xclip.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// writeClipboard sets the contents of the primary clipboard.
func writeClipboard(contents string) error {
	xclip := exec.Command("xclip")
	stdin, err := xclip.StdinPipe()
	if err != nil {
		return err
	}
	xclip.Start()

	if _, err = stdin.Write([]byte(contents)); err != nil {
		return err
	}
	stdin.Close()
	xclip.Wait()

	log.Debugf("writeClipboard: Set clipboard to %s", contents)
	return nil
}

// publishToServer opens a socket to the server and publishes the contents.
func publishToServer(contents string) error {
	conn, err := net.Dial("unix", sockFile)
	if err != nil {
		log.Errorf("publishToServer: dial error: %v", err)
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
func subscribeToServer(clip *clipboard) {
	for {
		// Create connection.
		buf := make([]byte, bufSize)
		conn, err := net.Dial("unix", sockFile)
		if err != nil {
			log.Errorf("subcribeToServer: dial error: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		log.Infof("subcribeToServer: Connected to %s", sockFile)

		// Send Subscribe command.
		if _, err := fmt.Fprintln(conn, "SUB"); err != nil {
			log.Infof("subscribeToServer: Error writing to socket: %v\n", err)
			time.Sleep(10 * time.Second)
			continue
		}

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
	for {
		xclipboard, err := readClipboard()
		if err != nil {
			log.Errorf("publishClipboard: error reading clipboard: %v", err)
			time.Sleep(time.Second)
			continue
		}
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
	clip := &clipboard{}
	go subscribeToServer(clip)

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
