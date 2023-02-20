// This file is part of clipsync (C)2023 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"fmt"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/romana/rlog"
)

// pastecmd prints the first message from the server (all messages are sent
// with persist).
func pastecmd(cfg globalConfig, cryptPassword []byte) error {
	log.Debug("Got paste command")
	ch := make(chan string)

	broker, err := newBroker(cfg, func(client mqtt.Client, msg mqtt.Message) {
		var err error

		data := string(msg.Payload())

		if len(cryptPassword) > 0 {
			data, err = decrypt64(data, cryptPassword)
			if err != nil {
				log.Error(err)
				data = ""
			}
		}

		log.Debugf("Received from server: %s", redact.redact(data))
		ch <- data
	})
	if err != nil {
		return fmt.Errorf("Unable to connect to broker: %v", err)
	}

	// Wait for read return
	spub := <-ch
	fmt.Print(spub)
	broker.Disconnect(1)

	return nil
}
