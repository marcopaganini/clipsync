// This file is part of clipsync (C)2023 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"fmt"
	"io"
	"os"
)

// copycmd reads the stdin and sends it to the broker (server).
func copycmd(cfg globalConfig, instanceID string, cryptPassword []byte, filter bool) error {
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

	publish(broker, *cfg.topic, spub, instanceID, cryptPassword)
	if filter {
		fmt.Print(spub)
	}
	return nil
}
