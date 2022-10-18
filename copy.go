// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"fmt"
	"io"
	"os"

	log "github.com/sirupsen/logrus"
)

// copycmd reads the stdin and sends it to the broker (server).
func copycmd(cfg globalConfig, cryptPassword []byte, filter bool) error {
	log.Debug("Got copy command")
	broker, err := newBroker(cfg, nil)
	if err != nil {
		return fmt.Errorf("Unable to connect to broker: %v", err)
	}
	pub, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("Unable to read data from stdin: %v", err)
	}
	defer broker.Disconnect(1)
	spub := string(pub)

	log.Debugf("Sending from stdin to broker: %s", redact.redact(spub))
	publish(broker, *cfg.topic, spub, cryptPassword)
	if filter {
		fmt.Print(spub)
	}
	return nil
}
