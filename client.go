// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.
package main

import (
	"crypto/md5"
	"fmt"
	"os"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/patrickmn/go-cache"
	log "github.com/romana/rlog"
)

const (
	syncerLockFile = "/var/run/lock/clipsync-client.lock"
)

type delayedPublishChan struct {
	broker        mqtt.Client
	topic         string
	content       string
	cryptPassword []byte
}

// clientcmd activates "client" mode, syncing the local clipboard to the server
// and vice-versa. This function will only return in case of error.
func clientcmd(cfg globalConfig, clientcfg clientConfig, cryptPassword []byte) error {
	// Client mode only makes sense if the DISPLAY environment
	// variable is set (otherwise we don't have a clipboard to sync).
	if os.Getenv("DISPLAY") == "" {
		return fmt.Errorf("Client mode requires the DISPLAY variable to be set")
	}

	log.Infof("Starting client, server: %s", *cfg.server)

	xsel := &xselection{}
	hashcache := cache.New(24*time.Hour, 24*time.Hour)

	broker, err := newBroker(cfg, func(client mqtt.Client, msg mqtt.Message) {
		subHandler(client, msg, xsel, hashcache, *clientcfg.syncsel, cryptPassword)
	})

	if err != nil {
		return fmt.Errorf("Unable to connect to broker: %v", err)
	}

	// Loops forever sending any local clipboard changes to broker.
	clientloop(broker, xsel, clientcfg, *cfg.topic, cryptPassword)

	// This should never happen.
	return nil
}

// subHandler is called by when new data is available and updates the
// clipboard with the remote clipboard.
func subHandler(broker mqtt.Client, msg mqtt.Message, xsel *xselection, hashcache *cache.Cache, syncsel bool, cryptPassword []byte) {
	xprimary := xsel.getXPrimary("")

	var err error

	data := string(msg.Payload())
	if len(cryptPassword) > 0 {
		// Ignore duplicate encrypted messages as they should never happen.
		md5 := fmt.Sprintf("%x", md5.Sum(msg.Payload()))
		if _, found := hashcache.Get(md5); found {
			log.Debugf("Ignoring duplicate encrypted message: %s", data)
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
// contains a single accent character (", ', `, ^, etc). This is a workaround
// for Chrome in Linux where chrome sometimes overwrites the primary selection
// with a single accent when compose sequences are used. For further details
// on this bug, see https://bugs.chromium.org/p/chromium/issues/detail?id=1213325
//
// if syncSelections is set, keep both primary and clipboard selections in
// sync (i.e. setting one will also set the other). Note that the server
// only handles one version of the clipboard.
//
// Note: For now, reading and writing to the clipboard is somewhat of an
// expensive operation as it requires calling xclip. This will be changed in a
// future version, which should allow us to simplify this function.
func clientloop(broker mqtt.Client, xsel *xselection, clientcfg clientConfig, topic string, cryptPassword []byte) {
	var err error

	dpchan := make(chan delayedPublishChan, 1)
	go delayedPublish(dpchan)

	for {
		// Wait for primary or clipboard change.
		if cnotify() != 0 {
			log.Errorf("ClipNotify returned error. Will wait and retry.")
			time.Sleep(time.Duration(2) * time.Second)
			continue
		}

		// Restore the primary selection to the saved value if it contains
		// a single rune and chromeQuirk is set.
		xprimary := xsel.getXPrimary("")

		// Do nothing on xclip error/empty clipboard.
		if xprimary == "" {
			continue
		}

		memPrimary := xsel.getMemPrimary()

		// Restore the memory clipboard if:
		// 1) chromeQuirk is set and...
		// 2) The X clipboard contains a single character in a list of characters and...
		// 3) memClipboard does NOT contain a single unicode character (avoid loops).
		if *clientcfg.chromequirk && isAccent(xprimary) && !isAccent(memPrimary) {
			log.Debugf("Chrome quirk detected. Restoring primary to %s", redact.redact(memPrimary))
			xprimary = memPrimary
			if err := xsel.setXPrimary(memPrimary); err != nil {
				log.Errorf("Cannot write to primary selection: %v", err)
			}
		}

		// Sync primary and clipboard, if requested. This will change the
		// selections if sync is needed.
		var pub string

		if *clientcfg.syncsel {
			if pub, err = syncClips(broker, xsel, topic, xprimary, xsel.getXClipboard("text/plain")); err != nil {
				log.Errorf("Error syncing selections (primary/clipboard): %v", err)
			}
		} else if memPrimary != xprimary {
			// If no sync between primary and clipboard requested, Only publish
			// if the X clipboard does not match our last memory clipboard (a
			// change happened).
			xsel.setMemPrimary(xprimary)
			pub = xprimary
		}

		// Publish if needed. Delay publication until clipboard settles since
		// large selections would cause an excessive number of publications.
		if pub != "" {
			dpchan <- delayedPublishChan{
				broker:        broker,
				topic:         topic,
				content:       pub,
				cryptPassword: cryptPassword,
			}
		}
	}
}

// publish publishes the given string to the desired topic.
func publish(broker mqtt.Client, topic, s string, cryptPassword []byte) {
	// Set in-memory primary selection and publish to server.
	log.Debugf("Publishing primary selection: %s", redact.redact(s))

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

// delayedPublish runs as a goroutine and takes a channel of type
// delayedPublishChan.  Information received through the channel is stored
// internally, until a timeout happens, at which time that information is
// published. This prevents excessive publications, in particular when
// selecting large areas of text which would cause publish to be called
// repeatedly.
func delayedPublish(ch chan delayedPublishChan) {
	var dp delayedPublishChan
	for {
		select {
		// Save information locally when receiving from channel.
		case c := <-ch:
			dp = delayedPublishChan{
				broker:        c.broker,
				topic:         c.topic,
				content:       c.content,
				cryptPassword: c.cryptPassword,
			}
			continue

		case <-time.After(1 * time.Second):
			// Safeguard: Only publish if some content is available.
			if dp.content != "" {
				publish(dp.broker, dp.topic, dp.content, dp.cryptPassword)
				dp = delayedPublishChan{}
			}
		}
	}
}

// syncClips synchronize the primary selection to the clipboard (and vice-versa),
// and returns a non-blank string if it needs to be published.
func syncClips(broker mqtt.Client, xsel *xselection, topic, xprimary, xclipboard string) (string, error) {
	var pub string

	// Ignore blank returns as they could be an error in xclip or no
	// content in the clipboard with the desired mime-type.
	if xclipboard != "" && xclipboard != xsel.getMemClipboard() {
		log.Debugf("X clipboard: %s", redact.redact(xclipboard))
		log.Debugf("mem primary: %s", redact.redact(xsel.getMemPrimary()))

		log.Debugf("Syncing clipboard -> X PRIMARY and memory primary/clipboard")
		if err := xsel.setXPrimary(xclipboard); err != nil {
			return "", err
		}
		xsel.setMemPrimary(xclipboard)
		xsel.setMemClipboard(xclipboard)
		pub = xclipboard

		// X primary changed? Sync to memory and X clipboard.
	} else if xprimary != "" && xprimary != xsel.getMemPrimary() {
		log.Debugf("X primary: %s", redact.redact(xprimary))
		log.Debugf("mem clipboard: %s", redact.redact(xsel.getMemClipboard()))

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
		log.Debugf("requesting publication of: %s", redact.redact(pub))
	}
	return pub, nil
}

// isAccent returns true if the string is one of the accents chrome sets the clipboard to.
func isAccent(s string) bool {
	accents := map[string]bool{
		"´": true,
		"¨": true,
		"`": true,
		"~": true,
		"^": true,
	}
	_, ok := accents[s]
	return ok
}
