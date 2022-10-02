// client.go - Client functions for clipsync.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"
)

const (
	syncerLockFile = "/var/run/lock/clipsync-client.lock"
	// Clipboard Selection Types.
	selPrimary   = "primary"
	selClipboard = "clipboard"
)

// client contains a representation of a MQTT client.
type client struct {
	sync.RWMutex
	primary   string
	clipboard string
}

func (x *client) setMemPrimary(value string) {
	x.Lock()
	x.primary = value
	x.Unlock()
}

func (x *client) setMemClipboard(value string) {
	x.Lock()
	x.clipboard = value
	x.Unlock()
}

func (x *client) getMemPrimary() string {
	x.Lock()
	v := x.primary
	x.Unlock()
	return v
}
func (x *client) getMemClipboard() string {
	x.Lock()
	v := x.clipboard
	x.Unlock()
	return v
}

// getXSelection returns the contents of the chosen X selection.
func (x *client) getXSelection(sel, mimetype string) string {
	x.Lock()
	defer x.Unlock()

	// xclip will return an error on an empty clipboard, but
	// there's no portable way to fetch the return code. Being
	// that the case, we'll just ignore those (TODO: Fix this).
	args := []string{"-selection", sel, "-o"}
	if mimetype != "" {
		args = append(args, "-t", mimetype)
	}
	xclip := exec.Command("xclip", args...)
	out, err := xclip.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// setXSelection sets the contents of the chosen X selection.
func (x *client) setXSelection(sel string, contents string) error {
	x.Lock()
	defer x.Unlock()

	xclip := exec.Command("xclip", "-selection", sel, "-i")
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

	log.Debugf("Set selection(%s) to: %s", sel, redact.redact(contents))
	return nil
}

// Syntactic sugar functions to access the X clipboard.

func (x *client) setXClipboard(contents string) error {
	return x.setXSelection(selClipboard, contents)
}

func (x *client) setXPrimary(contents string) error {
	return x.setXSelection(selPrimary, contents)
}

func (x *client) getXPrimary(mimetype string) string {
	return x.getXSelection(selPrimary, mimetype)
}

func (x *client) getXClipboard(mimetype string) string {
	return x.getXSelection(selClipboard, mimetype)
}

// subHandler is called by MQTT when new data is available and updates the
// clipboard with the remote clipboard.
func (x *client) subHandler(client mqtt.Client, msg mqtt.Message) {
	log.Debugf("Entered subHandler")

	primary := x.getMemPrimary()
	data := string(msg.Payload())

	log.Debugf("Received from server: %s", redact.redact(data))
	log.Debugf("Current memory primary selection: %s", redact.redact(primary))

	if data != primary {
		// Don't set the in-memory primary. This will cause clientloop
		// to notice a diff and sync primary and clipboard, if requested.
		log.Debugf("Values differ. Writing to primary.")
		if err := x.setXPrimary(data); err != nil {
			log.Errorf("Unable to set local primary selection: %v", err)
		}
	}
	log.Debugf("Leaving subHandler")
}

// clientloop periodically reads from this machine's primary selection
// and updates the MQTT server when changes happen. This function never
// returns.
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
func clientloop(broker mqtt.Client, topic string, pollTime int, cli *client, chromeQuirk bool, syncSelections bool) {
	for {
		time.Sleep(time.Duration(pollTime) * time.Second)

		// Restore the primary selection to the saved value if it contains
		// a single rune and chromeQuirk is set.
		xprimary := cli.getXPrimary("")
		memPrimary := cli.getMemPrimary()

		if chromeQuirk && utf8.RuneCountInString(xprimary) == 1 {
			log.Debugf("Chrome quirk detected. Restoring primary to %s", redact.redact(memPrimary))
			xprimary = memPrimary
			if err := cli.setXPrimary(memPrimary); err != nil {
				log.Errorf("Cannot write to primary selection: %v", err)
			}
		}

		// Sync primary and clipboard, if requested. This will change the
		// selections if sync is needed.
		if syncSelections {
			pub, err := syncPrimaryAndClip(broker, xprimary, cli.getXClipboard("text/plain"), cli)
			if err != nil {
				log.Errorf("Error syncing selections (primary/clipboard): %v", err)
			}
			// Publish to server, if needed
			if pub != "" {
				log.Debugf("Publishing result of syncPrimaryAndCLip: %s", redact.redact(pub))
				if token := broker.Publish(topic, 0, true, pub); token.Wait() && token.Error() != nil {
					log.Errorf("Error publishing result of syncPrimaryAndClip: %v", token.Error())
				}
				log.Debugf("Publish done")
			}
			continue
		}

		// Only publish if our original clipboard has changed.
		if cli.getMemPrimary() != xprimary {
			// Set in-memory primary selection and publish to server.
			cli.setMemPrimary(xprimary)
			log.Debugf("Publishing primary selection: %s", redact.redact(xprimary))
			if token := broker.Publish(topic, 0, true, xprimary); token.Wait() && token.Error() != nil {
				log.Errorf("Error publishing primary selection: %v", token.Error())
			}
			log.Debugf("Publish done")
		}
	}
}

// syncPrimaryAndClip synchronizes the primary selection to the clipboard (and vice-versa).
func syncPrimaryAndClip(broker mqtt.Client, xprimary, xclipboard string, cli *client) (string, error) {
	// X clipboard changed? Sync to memory and X primary selection.
	// Ignore blank returns as they could be an error in xclip or no
	// content in the clipboard with the desired mime-type.
	if xclipboard != "" && xclipboard != cli.getMemClipboard() {
		cli.setMemPrimary(xclipboard)
		cli.setMemClipboard(xclipboard)
		log.Debugf("Syncing clipboard to X PRIMARY and memory primary/clipboard")
		if err := cli.setXPrimary(xclipboard); err != nil {
			return "", err
		}
		return xclipboard, nil
	}

	// X primary changed? Sync to memory and X clipboard.
	if xprimary != "" && xprimary != cli.getMemPrimary() {
		// primary changed, sync to clipboard.
		cli.setMemPrimary(xprimary)
		cli.setMemClipboard(xprimary)
		log.Debugf("Syncing primary to X CLIPBOARD and memory primary/clipboard")
		if err := cli.setXClipboard(xprimary); err != nil {
			return "", err
		}
		return xprimary, nil
	}
	return "", nil
}
