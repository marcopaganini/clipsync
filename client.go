// This file is part of clipsync (C)2023 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.
package main

import (
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"errors"
	"fmt"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/patrickmn/go-cache"
	log "github.com/romana/rlog"
)

type delayedPublishChan struct {
	broker        mqtt.Client
	topic         string
	content       string
	instanceID    string
	cryptPassword []byte
}

// Lineformat contains the line format for mqtt messages. All attributes must
// be exported since this will be serialized into something else before transmission.
type Lineformat struct {
	InstanceID string
	Message    string
}

// mqttCallback represents the elements from a mqtt.newBroker callback.
type mqttCallback struct {
	client mqtt.Client
	msg    mqtt.Message
}

// Global mutex used across client functions before they access the clipboard.
// This avoids race conditions between subHandler and clientloop.
var globalMutex sync.Mutex

// clientcmd activates "client" mode, syncing the local clipboard to the server
// and vice-versa. This function will only return in case of error.
func clientcmd(cfg globalConfig, clientcfg clientConfig, instanceID string, cryptPassword []byte) error {
	incoming := make(chan mqttCallback, 10)

	log.Infof("Starting client, server: %s", *cfg.server)

	xsel := &xselection{}
	hashcache := cache.New(24*time.Hour, 24*time.Hour)

	// subHandler blocks on a buffered channel and newBroker feeds the channel with the
	// relevant information from the callback. The function called by newBroker cannot
	// block, or it will deadlock the receipt of messages from MQTT.
	go subHandler(incoming, xsel, hashcache, *clientcfg.syncsel, instanceID, cryptPassword)
	broker, err := newBroker(cfg, func(client mqtt.Client, msg mqtt.Message) {
		incoming <- mqttCallback{
			client: client,
			msg:    msg}
	})

	if err != nil {
		return fmt.Errorf("Unable to connect to broker: %v", err)
	}

	// Loops forever sending any local clipboard changes to broker.
	clientloop(broker, xsel, clientcfg, *cfg.topic, instanceID, cryptPassword)

	// This should never happen.
	return nil
}

// subHandler runs as a goroutine and blocks reading on the main channel. Once
// information is available, it processes the incoming request.
func subHandler(incoming chan mqttCallback, xsel *xselection, hashcache *cache.Cache, syncsel bool, instanceID string, cryptPassword []byte) {
	for {
		ch := <-incoming
		globalMutex.Lock()

		payload := ch.msg.Payload()
		broker := ch.client

		data := string(payload)
		xprimary := xsel.getXPrimary("")

		var hash string

		if len(cryptPassword) > 0 {
			// Ignore duplicate encrypted messages as they should never happen.
			hash = fmt.Sprintf("%x", md5.Sum(payload))
			if _, found := hashcache.Get(hash); found {
				log.Debugf("Ignoring duplicate encrypted message: %s", data)
				globalMutex.Unlock()
				continue
			}
		}

		mqttmsg, err := decodeMQTT(data, cryptPassword)
		if err != nil {
			log.Debug(err)
			globalMutex.Unlock()
			continue
		}

		// At this point, we know we have a good message, If encryption was
		// used, save the hash in the cache so we can check for duplicated
		// encrypted messages later.
		if len(cryptPassword) > 0 {
			hashcache.Set(hash, true, cache.DefaultExpiration)
		}

		if mqttmsg.Message == "" {
			log.Debugf("Received zero-length data from server. Ignoring.")
			globalMutex.Unlock()
			continue
		}

		log.Debugf("Received from server [%s]: %s", mqttmsg.InstanceID, redact.redact(mqttmsg.Message))

		// Ignore this message if it's an echo from the mqtt server.
		if mqttmsg.InstanceID == instanceID {
			log.Debugf("Ignoring our own message from mqtt server.")
			globalMutex.Unlock()
			continue
		}

		log.Debugf("Current X primary selection: %s", redact.redact(xprimary))

		// This function only gets called if we have real data available, so we can
		// set the primary and memory clipboards directly if we have changes.
		log.Debugf("Server data != Current X primary selection. Writing to primary.")

		if err := xsel.setXPrimary(mqttmsg.Message); err != nil {
			log.Errorf("Unable to set X Primary selection: %v", err)
		}
		if syncsel {
			// We call syncClips with the new primary contents and set xclipboard
			// to getMemClipboard. This guarantee that we'll never sync from the
			// clipboard to the just received primary.
			if _, err := syncClips(broker, xsel, mqttmsg.Message, xsel.getMemClipboard()); err != nil {
				log.Debug(err)
				globalMutex.Unlock()
				continue
			}
		}
		globalMutex.Unlock()
	}
}

// decodeMQTT decodes a gob encoded Lineformat object (read from MQTT) and
// attempts to decrypt it if a cryptPassword was specified. Returns the
// (unencrypted) Lineformat object.
func decodeMQTT(data string, cryptPassword []byte) (Lineformat, error) {
	var err error

	plain := data
	if len(cryptPassword) > 0 {
		plain, err = decrypt64(data, cryptPassword)
		if err != nil {
			return Lineformat{}, err
		}
	}
	if plain == "" {
		return Lineformat{}, errors.New("ignoring zero-length message received from broker")
	}

	// At this point plain contains a gob encoded Lineformat structure.
	buf := bytes.NewBufferString(plain)
	dec := gob.NewDecoder(buf)
	var mqttmsg Lineformat
	if err = dec.Decode(&mqttmsg); err != nil {
		return Lineformat{}, fmt.Errorf("Error decoding MQTT message: %v", err)
	}
	return mqttmsg, nil
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
func clientloop(broker mqtt.Client, xsel *xselection, clientcfg clientConfig, topic, instanceID string, cryptPassword []byte) {
	dpchan := make(chan delayedPublishChan, 1)
	go delayedPublish(dpchan)

	for {
		// Wait for primary or clipboard change.
		if cnotify() != 0 {
			log.Errorf("ClipNotify returned error. Will wait and retry.")
			time.Sleep(time.Duration(2) * time.Second)
			globalMutex.Unlock()
			continue
		}
		globalMutex.Lock()

		xprimary := xsel.getXPrimary("")
		xclipboard := xsel.getXClipboard("text/plain")
		log.Debugf("Clipboard CHANGED: primary=%s, clipboard=%s", redact.redact(xprimary), redact.redact(xclipboard))

		// Do nothing on xclip error/empty clipboard.
		if xprimary == "" {
			globalMutex.Unlock()
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
			var err error
			pub, err = syncClips(broker, xsel, xprimary, xclipboard)
			if err != nil {
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
				instanceID:    instanceID,
				cryptPassword: cryptPassword,
			}
		}
		globalMutex.Unlock()
	}
}

// publish forms a Lineformat message using the instanceID and string, and
// publishes it to the desired topic. This message does not return errors,
// but logs them using log.Debugf().
func publish(broker mqtt.Client, topic, s, instanceID string, cryptPassword []byte) {
	// Set in-memory primary selection and publish to server.
	log.Debugf("Publishing primary selection [%s]: %s", instanceID, redact.redact(s))

	// Encode message and instance ID.
	var buf bytes.Buffer
	mqttmsg := Lineformat{
		InstanceID: instanceID,
		Message:    s,
	}
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(mqttmsg)
	if err != nil {
		log.Error(err)
		return
	}

	var cryptdata string
	if len(cryptPassword) > 0 {
		cryptdata, err = encrypt64(buf.String(), cryptPassword)
		if err != nil {
			log.Error(err)
			return
		}
	}

	if token := broker.Publish(topic, 0, true, cryptdata); token.Wait() && token.Error() != nil {
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
				instanceID:    c.instanceID,
				cryptPassword: c.cryptPassword,
			}
			continue

		case <-time.After(1 * time.Second):
			// Safeguard: Only publish if some content is available.
			if dp.content != "" {
				publish(dp.broker, dp.topic, dp.content, dp.instanceID, dp.cryptPassword)
				dp = delayedPublishChan{}
			}
		}
	}
}

// syncClips synchronize the primary selection to the clipboard (and vice-versa),
// and returns a non-blank string if it needs to be published.
func syncClips(broker mqtt.Client, xsel *xselection, xprimary, xclipboard string) (string, error) {
	var pub string

	// Ignore blank returns as they could be an error in xclip or no
	// content in the clipboard with the desired mime-type.
	memClipboard := xsel.getMemClipboard()
	memPrimary := xsel.getMemPrimary()

	log.Tracef(1, "X primary: %s", redact.redact(xprimary))
	log.Tracef(1, "X clipboard: %s", redact.redact(xclipboard))
	log.Tracef(1, "Memory primary: %s", redact.redact(memPrimary))
	log.Tracef(1, "Memory clipboard: %s", redact.redact(memClipboard))

	if xclipboard != "" && xclipboard != memClipboard {
		// Only copy clipboard to primary if they differ. Otherwise, we end up
		// losing the "selected" mark (inverted text) on some programs, notably
		// gnome terminal (and possibly other vte based terminals,) which
		// always sets both the clipboard and primary on select.
		log.Debugf("Syncing X clipboard -> X primary")
		if xclipboard != xprimary {
			log.Debugf("Setting X primary = X clipboard: %s", redact.redact(xclipboard))
			if err := xsel.setXPrimary(xclipboard); err != nil {
				return "", err
			}
		}
		log.Tracef(1, "Setting mem primary = X clipboard: %s", redact.redact(xclipboard))
		log.Tracef(1, "Setting mem clipboard = X clipboard: %s", redact.redact(xclipboard))
		xsel.setMemPrimary(xclipboard)
		xsel.setMemClipboard(xclipboard)

		pub = xclipboard
	} else if xprimary != "" && xprimary != memPrimary {
		log.Debugf("Syncing X primary -> X clipboard")
		if xclipboard != xprimary {
			log.Debugf("Setting X clipboard = X primary: %s", redact.redact(xprimary))
			if err := xsel.setXClipboard(xprimary); err != nil {
				return "", err
			}
		}

		log.Tracef(1, "Setting mem clipboard = X primary: %s", redact.redact(xprimary))
		log.Tracef(1, "Setting mem primary = X primary: %s", redact.redact(xprimary))
		xsel.setMemClipboard(xprimary)
		xsel.setMemPrimary(xprimary)

		pub = xprimary
	}

	// Publish to server, if needed
	if pub != "" {
		log.Debugf("Requesting publication of: %s", redact.redact(pub))
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
