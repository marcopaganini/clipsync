// client.go - Client functions for clipsync.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"context"
	"fmt"
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
	// Timeout when running xclip, in ms.
	xclipTimeout = 1500
)

// client contains a representation of a MQTT client.
type client struct {
	sync.RWMutex
	primary        string
	clipboard      string
	topic          string
	syncSelections bool
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
	ctx, cancel := context.WithTimeout(context.Background(), xclipTimeout*time.Millisecond)
	defer cancel()

	xclip := exec.CommandContext(ctx, "xclip", args...)
	out, err := xclip.Output()
	if err != nil {
		log.Debugf("Error executing xclip: %v", err)
		return ""
	}
	return string(out)
}

// setXSelection sets the contents of the chosen X selection.
func (x *client) setXSelection(sel string, contents string) error {
	x.Lock()
	defer x.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), xclipTimeout*time.Millisecond)
	defer cancel()

	xclip := exec.CommandContext(ctx, "xclip", "-selection", sel, "-i")
	stdin, err := xclip.StdinPipe()
	if err != nil {
		return fmt.Errorf("Error reading xclip stdin: %v", err)
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
func (x *client) subHandler(broker mqtt.Client, msg mqtt.Message) {
	log.Debugf("Entering subHandler")
	defer log.Debugf("Leaving subHandler")

	xprimary := x.getXPrimary("")
	data := string(msg.Payload())

	log.Debugf("Received from server: %s", redact.redact(data))
	log.Debugf("Current X primary selection: %s", redact.redact(xprimary))

	// Same data we already have? Return.
	if data == xprimary {
		log.Debugf("Server data and primary selection are identical. Returning.")
		return
	}

	// This function only gets called if we have real data available, so we can
	// set the primary and memory clipboards directly if we have changes.
	log.Debugf("Server data != Current X primary selection. Writing to primary.")
	if err := x.setXPrimary(data); err != nil {
		log.Errorf("Unable to set X Primary selection: %v", err)
	}
	x.setMemPrimary(data)

	if x.syncSelections && x.getXClipboard("text/plain") != data {
		log.Debugf("Primary <-> Clipboard sync requested. Setting clipboard.")
		if err := x.setXClipboard(data); err != nil {
			log.Errorf("Unable to set X Clipboard: %v", err)
		}
		x.setMemClipboard(data)
	}

	publish(broker, x.topic, data)
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
func clientloop(broker mqtt.Client, topic string, pollTime int, cli *client, chromeQuirk bool) {
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
		if cli.syncSelections {
			if err := syncClipsAndPublish(broker, topic, xprimary, cli.getXClipboard("text/plain"), cli); err != nil {
				log.Errorf("Error syncing selections (primary/clipboard): %v", err)
			}
		} else if memPrimary != xprimary {
			// If no sync between primary and clipboard requested, Only publish
			// if the X clipboard does not match our last memory clipboard (a
			// change happened).
			cli.setMemPrimary(xprimary)
			publish(broker, topic, xprimary)
		}
	}
}

// publish publishes the given string to the desired topic.
func publish(broker mqtt.Client, topic, s string) {
	// Set in-memory primary selection and publish to server.
	log.Debugf("Publishing primary selection: %s", redact.redact(s))
	if token := broker.Publish(topic, 0, true, s); token.Wait() && token.Error() != nil {
		log.Errorf("Error publishing to server: %v", token.Error())
	}
	log.Debugf("Publish done")
}

// syncClipsAndPublish synchronize the primary selection to the clipboard (and vice-versa),
// and publishes results (if needed).
func syncClipsAndPublish(broker mqtt.Client, topic, xprimary, xclipboard string, cli *client) error {
	var pub string

	// Ignore blank returns as they could be an error in xclip or no
	// content in the clipboard with the desired mime-type.
	if xclipboard != "" && xclipboard != cli.getMemClipboard() {
		log.Debugf("syncClipsAndPublish X clipboard: %s", redact.redact(xclipboard))
		log.Debugf("syncClipsAndPublish mem primary: %s", redact.redact(cli.getMemPrimary()))

		log.Debugf("Syncing clipboard -> X PRIMARY and memory primary/clipboard")
		if err := cli.setXPrimary(xclipboard); err != nil {
			return err
		}
		cli.setMemPrimary(xclipboard)
		cli.setMemClipboard(xclipboard)
		pub = xclipboard

		// X primary changed? Sync to memory and X clipboard.
	} else if xprimary != "" && xprimary != cli.getMemPrimary() {
		log.Debugf("syncClipsAndPublish X primary: %s", redact.redact(xprimary))
		log.Debugf("syncClipsAndPublish mem clipboard: %s", redact.redact(cli.getMemClipboard()))

		log.Debugf("Syncing primary -> X CLIPBOARD and memory primary/clipboard")
		if err := cli.setXClipboard(xprimary); err != nil {
			return err
		}
		// primary changed, sync to clipboard.
		cli.setMemPrimary(xprimary)
		cli.setMemClipboard(xprimary)
		pub = xprimary
	}

	// Publish to server, if needed
	if pub != "" {
		log.Debugf("syncPrimaryAndClip publishing: %s", redact.redact(pub))
		publish(broker, topic, pub)
	}
	return nil
}
