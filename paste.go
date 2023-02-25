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
func pastecmd(cfg globalConfig, instanceID string, cryptPassword []byte) error {
	ch := make(chan string)

	broker, err := newBroker(cfg, func(client mqtt.Client, msg mqtt.Message) {
		data := string(msg.Payload())

		mqttmsg, err := decodeMQTT(data, cryptPassword)
		if err != nil {
			log.Debug(err)
			ch <- ""
			return
		}
		log.Debugf("Received from server [%s]: %s", mqttmsg.InstanceID, redact.redact(mqttmsg.Message))
		ch <- mqttmsg.Message
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
