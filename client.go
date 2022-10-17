// client.go - Client functions for clipsync.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"crypto/md5"
	"fmt"
	"regexp"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/patrickmn/go-cache"
	log "github.com/sirupsen/logrus"
)

const (
	syncerLockFile = "/var/run/lock/clipsync-client.lock"
)

// client contains a representation of a MQTT client.
type client struct {
	sync.RWMutex
	primary        string
	clipboard      string
	topic          string
	syncSelections bool
	cryptPassword  []byte
}

// newClient returns a new client with some sane defaults.
func newClient(topic string, syncSelections bool, cryptPassword []byte) *client {
	return &client{
		topic:          topic,
		syncSelections: syncSelections,
		cryptPassword:  cryptPassword,
	}
}

// subHandler is called by when new data is available and updates the
// clipboard with the remote clipboard.
func subHandler(broker mqtt.Client, msg mqtt.Message, xsel *xselection, hashcache *cache.Cache, syncsel bool, cryptPassword []byte) {
	log.Debugf("Entering subHandler")
	defer log.Debugf("Leaving subHandler")

	xprimary := xsel.getXPrimary("")

	var err error

	data := string(msg.Payload())
	if len(cryptPassword) > 0 {
		// Ignore duplicate encrypted messages as they should never happen.
		md5 := fmt.Sprintf("%x", md5.Sum(msg.Payload()))
		if _, found := hashcache.Get(md5); found {
			log.Warningf("Ignoring duplicate encrypted message: %s", data)
			return
		}
		data, err = decrypt64(data, cryptPassword)
		if err != nil {
			log.Error(err)
			return
		}
		if data == "" {
			log.Debugf("Received zero-length encrypted payload from server. Ignoring.")
			return
		}
		// At this point, we have a good encrypted message, so save the hash in
		// the cache.
		hashcache.Set(md5, true, cache.DefaultExpiration)
	}

	if data == "" {
		log.Debugf("Received zero-length data from server. Ignoring.")
		return
	}

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
	if err := xsel.setXPrimary(data); err != nil {
		log.Errorf("Unable to set X Primary selection: %v", err)
	}
	xsel.setMemPrimary(data)

	if syncsel && xsel.getXClipboard("text/plain") != data {
		log.Debugf("Primary <-> Clipboard sync requested. Setting clipboard.")
		if err := xsel.setXClipboard(data); err != nil {
			log.Errorf("Unable to set X Clipboard: %v", err)
		}
		xsel.setMemClipboard(data)
	}
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
func clientloop(broker mqtt.Client, cli *client, xsel *xselection, topic string, pollTime int, chromeQuirk bool) {
	var singleUnicode = regexp.MustCompile(`^[[:^ascii:]]$`)
	var err error

	for {
		time.Sleep(time.Duration(pollTime) * time.Second)

		// Restore the primary selection to the saved value if it contains
		// a single rune and chromeQuirk is set.
		xprimary := xsel.getXPrimary("")

		// Do nothing on xclip error/empty clipboard.
		if xprimary == "" {
			continue
		}

		memPrimary := xsel.getMemPrimary()

		// Restore the memory clipboard if:
		// 1) chromeQuirk is set and
		// 2) The X clipboard contains a single unicode characters and
		// 3) memClipboard does NOT contain a single unicode character (avoid loops).
		if chromeQuirk && singleUnicode.MatchString(xprimary) && !singleUnicode.MatchString(memPrimary) {
			log.Debugf("Chrome quirk detected. Restoring primary to %s", redact.redact(memPrimary))
			xprimary = memPrimary
			if err := xsel.setXPrimary(memPrimary); err != nil {
				log.Errorf("Cannot write to primary selection: %v", err)
			}
		}

		// Sync primary and clipboard, if requested. This will change the
		// selections if sync is needed.

		var pub string

		if cli.syncSelections {
			if pub, err = syncClips(broker, cli, xsel, topic, xprimary, xsel.getXClipboard("text/plain")); err != nil {
				log.Errorf("Error syncing selections (primary/clipboard): %v", err)
			}
		} else if memPrimary != xprimary {
			// If no sync between primary and clipboard requested, Only publish
			// if the X clipboard does not match our last memory clipboard (a
			// change happened).
			xsel.setMemPrimary(xprimary)
			pub = xprimary
		}

		// Publish if needed.
		if pub != "" {
			publish(broker, topic, pub, cli.cryptPassword)
		}
	}
}

// publish publishes the given string to the desired topic.
func publish(broker mqtt.Client, topic, s string, cryptPassword []byte) {
	// Set in-memory primary selection and publish to server.
	log.Debugf("Publishing primary selection: %s", redact.redact(s))
	defer log.Debugf("Publish done")

	var err error
	if len(cryptPassword) > 0 {
		s, err = encrypt64(s, cryptPassword)
		if err != nil {
			log.Error(err)
			return
		}
	}

	if token := broker.Publish(topic, 0, true, s); token.Wait() && token.Error() != nil {
		log.Errorf("Error publishing to server: %v", token.Error())
	}
}

// syncClips synchronize the primary selection to the clipboard (and vice-versa),
// and returns a non-blank string if it needs to be published.
func syncClips(broker mqtt.Client, cli *client, xsel *xselection, topic, xprimary, xclipboard string) (string, error) {
	var pub string

	// Ignore blank returns as they could be an error in xclip or no
	// content in the clipboard with the desired mime-type.
	if xclipboard != "" && xclipboard != xsel.getMemClipboard() {
		log.Debugf("syncClips X clipboard: %s", redact.redact(xclipboard))
		log.Debugf("syncClips mem primary: %s", redact.redact(xsel.getMemPrimary()))

		log.Debugf("Syncing clipboard -> X PRIMARY and memory primary/clipboard")
		if err := xsel.setXPrimary(xclipboard); err != nil {
			return "", err
		}
		xsel.setMemPrimary(xclipboard)
		xsel.setMemClipboard(xclipboard)
		pub = xclipboard

		// X primary changed? Sync to memory and X clipboard.
	} else if xprimary != "" && xprimary != xsel.getMemPrimary() {
		log.Debugf("syncClips X primary: %s", redact.redact(xprimary))
		log.Debugf("syncClips mem clipboard: %s", redact.redact(xsel.getMemClipboard()))

		log.Debugf("Syncing primary -> X CLIPBOARD and memory primary/clipboard")
		if err := xsel.setXClipboard(xprimary); err != nil {
			return "", err
		}
		// primary changed, sync to clipboard.
		xsel.setMemPrimary(xprimary)
		xsel.setMemClipboard(xprimary)
		pub = xprimary
	}

	// Publish to server, if needed
	if pub != "" {
		log.Debugf("syncClips requesting publication of: %s", redact.redact(pub))
	}
	return pub, nil
}
