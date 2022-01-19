// This file is part of clipshare (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipshare for details.

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"
)

// removeSocket removes an existing socket file, if it exists.
func removeSocket(sockfile string) error {
	// Remove the existing socket file if it exists.
	if _, err := os.Stat(sockfile); err == nil {
		if err := os.Remove(sockfile); err != nil && err != os.ErrNotExist {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// server starts a local server for read/write operations to the clipboard file.
func server() error {
	// clip holds the contents of the clipboard for get/set operations.
	clip := &clipboard{}

	log.Infof("Starting server")

	if err := removeSocket(sockFile); err != nil {
		return fmt.Errorf("Error removing socket file (%s): %v", sockFile, err)
	}

	mask := syscall.Umask(0077)
	listen, err := net.Listen("unix", sockFile)
	if err != nil {
		syscall.Umask(mask)
		return fmt.Errorf("Listen error: %v", err)
	}

	// Signal handling.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func(listen net.Listener, c chan os.Signal) {
		sig := <-c
		log.Infof("Caught signal %s: shutting down.", sig)
		listen.Close()
		os.Exit(0)
	}(listen, sig)

	id := 0
	log.Infof("Starting accept loop")

	remoteMsg := map[int]chan string{}

	// Accept returns a new connection for each new connection to this server.
	for {
		conn, err := listen.Accept()
		syscall.Umask(mask)
		if err != nil {
			return fmt.Errorf("Accept error: %v", err)
		}
		remoteMsg[id] = make(chan string)

		go serverHandler(id, conn, clip, remoteMsg)
		id++
	}
}

// serverHandler handles client requests.
//
// For every new connection, server() calls this function with a numeric unique
// id, a new connection, a copy of the in-memory clipboard, and a map of string
// channels, keyed by id.
//
// This function will read one record from the input and look for a PUB or SUB
// request. For PUB requests, it sets the in-memory version of the clipboard
// and broadcast it to all channels in the remoteMsg map.
//
// For SUB requests, it prints the current version of the clipboard and sits
// waiting for changes to its own channel (keyed by id) in the remoteMsg map,
// sending its content to the client when that happens.
func serverHandler(id int, conn net.Conn, clip *clipboard, remoteMsg map[int]chan string) {
	log.Debugf("handler(%d): Starting.", id)
	buf := make([]byte, bufSize)

	// Wait for the command from the remote:
	// PUB: Publish -> Read data after command and publish to all clients.
	// SUB: Subscribe -> Enter infinite loop and print every clipboard change.
	// PRINT: Print the current in-memory clipboard and exit.
	nbytes, err := conn.Read(buf)
	if err != nil {
		if err == io.EOF {
			log.Infof("handler(%d): Connection closed by client.", id)
			return
		}
		log.Errorf("handler(%d): Error reading socket: %v", id, err)
		return
	}

	data := string(buf[0:nbytes])

	switch {
	// Publish Request: set the current clipboard to the value read from the
	// socket and broadcast it to all other connections. Close the connection
	// afterwards.
	case strings.HasPrefix(data, "PUB\n"):
		log.Infof("handler(%d): Publish request received.", id)
		log.Debugf("handler(%d): Received value: %q", id, data)

		// Update in-memory clipboard.
		data = data[4:nbytes]
		clip.set(data)

		// Update all other instances.
		for k, c := range remoteMsg {
			log.Debugf("handler(%d): Updating handler id %d", id, k)
			c <- clip.get()
		}

		log.Debugf("handler(%d): Closing connection after PUB command.", id)
		delete(remoteMsg, id)
		conn.Close()
		return

	// Subscribe request: Print the initial value of the memory clipboard and
	// every change from this point on. We expect clients to read forever on
	// this socket.
	case strings.HasPrefix(data, "SUB\n"):
		log.Infof("handler(%d): Subscribe request received. Waiting for updates.", id)

		// Send initial clipboard contents.
		log.Debugf("handler(%d): Initial send of memory clipboard contents.", id)
		_, err := conn.Write([]byte(clip.get()))
		if err != nil {
			log.Errorf("handler(%d): Error writing socket: %v", id, err)
		}

		for {
			// Wait for updates to my id in the map of channels.
			contents := <-remoteMsg[id]
			log.Debugf("handler(%d): Got update request for %s", id, contents)
			_, err := conn.Write([]byte(contents))
			if err != nil {
				log.Errorf("handler(%d): Error writing socket: %v", id, err)
				break
			}
		}

	// Print the in-memory clipboard and exit.
	case strings.HasPrefix(data, "PRINT\n"):
		log.Infof("handler(%d): Print request received.", id)

		_, err := conn.Write([]byte(clip.get()))
		if err != nil {
			log.Errorf("handler(%d): Error writing socket: %v", id, err)
		}
		log.Debugf("handler(%d): Closing connection after PRINT command.", id)

	// Unknown command.
	default:
		log.Errorf("handler(%d): Received unknown command %q", id, data)
	}

	delete(remoteMsg, id)
	conn.Close()
}

// printServerClipboard sends a request to the server to print its internal
// representation of the clipboard.
func printServerClipboard() (string, error) {
	buf := make([]byte, bufSize)
	conn, err := net.Dial("unix", sockFile)
	if err != nil {
		log.Errorf("printServerClipboard: dial error: %v", err)
		return "", err
	}
	if _, err := fmt.Fprintf(conn, "PRINT\n"); err != nil {
		log.Errorf("printServerClipboard: Error writing to socket: %v", err)
	}
	// Read one record and print it.
	nbytes, err := conn.Read(buf)
	if err != nil {
		if err == io.EOF {
			log.Infof("printServerClipboard: Connection closed by server.")
		} else {
			log.Errorf("printServerClipboard: Error reading socket: %v", err)
		}
		return "", err
	}
	conn.Close()
	return string(buf[0:nbytes]), nil
}
