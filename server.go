// server.go - Server functions for clipsync.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fredli74/lockfile"
	log "github.com/sirupsen/logrus"
)

const (
	serverLockFile = "/var/run/lock/clipsync-server.lock"

	// Timeout for accept, in seconds.
	serverConnectionTimeout = 3
)

// sockPath returns the full path to the socket file.
func sockPath(name string) (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("sockPath: environment variable HOME not set")
	}
	return filepath.Join(home, name), nil
}

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

// singleInstanceOrDie guarantees that this is the only instance of
// this program using the specified lockfile. Caller must call
// Unlock on the returned lock once it's not needed anymore.
func singleInstanceOrDie(lckfile string) *lockfile.LockFile {
	lock, err := lockfile.Lock(lckfile)
	if err != nil {
		log.Fatalf("Another instance is already running.")
	}
	return lock
}

// socketListen removes any existing socketfiles named 'sockfile' and creates a
// new unix domain socket using net.Listen. The file is chmoded 600 for
// security reasons.
func socketListen(sockfile string) (net.Listener, error) {
	log.Infof("Starting server on socket %s", sockfile)
	if err := removeSocket(sockfile); err != nil {
		return nil, fmt.Errorf("error removing socket file (%s): %v", sockfile, err)
	}

	listen, err := net.Listen("unix", sockfile)
	if err != nil {
		return nil, fmt.Errorf("listen error: %v", err)
	}
	if err := os.Chmod(sockfile, 0600); err != nil {
		return nil, fmt.Errorf("chmod error: %v", err)
	}
	return listen, nil
}

// sigTermHandler sets a signal handler to close the listener on SIGTERM and
// issue an appropriate message to the user. This functino will exit the
// program if sigterm is received.
func sigTermHandler(listen net.Listener) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func(listen net.Listener, c chan os.Signal) {
		sig := <-c
		log.Infof("Caught signal %s: shutting down.", sig)
		listen.Close()
		os.Exit(0)
	}(listen, sig)
}

// server starts a local clipboard server.
func server(sockfile string) error {
	lock := singleInstanceOrDie(serverLockFile)
	defer lock.Unlock()

	// sel holds the contents of the primary selection for get/set operations.
	sel := &selection{}

	listen, err := socketListen(sockfile)
	if err != nil {
		return fmt.Errorf("server: %v", err)
	}

	// Signal handling.
	sigTermHandler(listen)

	id := 0
	log.Infof("Starting accept loop.")

	remoteMsg := map[int]chan string{}

	for {
		// Accept returns a new connection for each new connection to this
		// server. We process the commands here and dispatch the long
		// lived actions in a gorouting (currently, Subscribe).
		conn, err := listen.Accept()
		if err != nil {
			return fmt.Errorf("server: Accept error: %v", err)
		}

		buf := make([]byte, bufSize)

		// Read command from client.
		conn.SetDeadline(time.Now().Add(serverConnectionTimeout * time.Second))
		nbytes, err := conn.Read(buf)
		if err != nil {
			switch {
			case err == io.EOF:
				log.Infof("Client closed socket.")
				fallthrough
			case os.IsTimeout(err):
				log.Infof("Client timed out. Closing socket.")
				conn.Close()
				continue
			default:
				return fmt.Errorf("server: Error reading socket: %v", err)
			}
		}
		data := string(buf[0:nbytes])

		switch {
		// Publish Request: set the current primary selection to the value read
		// from the socket and broadcast it to all other connections. Close the
		// connection afterwards.
		case strings.HasPrefix(data, "PUB\n"):
			log.Infof("Publish request received.")
			log.Debugf("Received value: %q", data)

			// Update in-memory primary selection.
			data = data[4:nbytes]
			sel.setPrimary(data)

			// Update all other instances.
			for k, c := range remoteMsg {
				log.Debugf("Updating handler id %d", k)
				c <- sel.getPrimary()
			}

			log.Debugf("Closing connection after PUB command.")
			conn.Close()

		case strings.HasPrefix(data, "SUB\n"):
			log.Infof("Subscribe request received (id=%d). Waiting for updates.", id)
			remoteMsg[id] = make(chan string)

			// Reset connection deadline (SUB is a long standing connection).
			conn.SetDeadline(time.Time{})
			go subHandler(id, conn, sel, remoteMsg)
			id++

		// Print the in-memory primary selection and exit.
		case strings.HasPrefix(data, "PRINT\n"):
			log.Infof("Print request received.")

			_, err := conn.Write([]byte(sel.getPrimary()))
			if err != nil {
				log.Errorf("Error writing socket: %v", err)
			}
			log.Debugf("Closing connection after PRINT command.")
			conn.Close()

		// Unknown command.
		default:
			log.Errorf("Received unknown command: %q", data)
			conn.Close()
		}
	}
}

// subHandler handles SUB requests.
//
// For every new connection with a SUB request, server() calls this function
// with a numeric unique id, a new connection, a copy of the in-memory primary
// selection, and a map of string channels, keyed by id.
//
// This function will send the current state of the primary selection and wait
// forever on remoteMsg, writing to the socket any messages published by other
// clients.
func subHandler(id int, conn net.Conn, clip *selection, remoteMsg map[int]chan string) {
	log.Debugf("Handler %d: Starting.", id)

	// Subscribe request: Print the initial value of the in-memory primary
	// selection and every change from this point on. We expect clients to read
	// forever on this socket.

	// Send initial primary selection contents.
	log.Debugf("Handler %d: Initial send of in-memory primary selection contents.", id)
	_, err := conn.Write([]byte(clip.getPrimary()))
	if err != nil {
		log.Errorf("Handler %d: Error writing socket: %v", id, err)
	}

	for {
		// Wait for updates to my id in the map of channels.
		contents := <-remoteMsg[id]
		log.Debugf("Handler %d: Got update request for %s", id, contents)
		_, err := conn.Write([]byte(contents))
		if err != nil {
			log.Errorf("Handler %d: Error writing socket: %v", id, err)
			break
		}
	}

	delete(remoteMsg, id)
	conn.Close()
}

// printServerClipboard sends a request to the server to print its internal
// representation of the clipboard.
func printServerClipboard(sockfile string) (string, error) {
	buf := make([]byte, bufSize)
	conn, err := net.Dial("unix", sockfile)
	if err != nil {
		log.Error(err)
		return "", err
	}
	if _, err := fmt.Fprintf(conn, "PRINT\n"); err != nil {
		log.Errorf("Error writing to socket: %v", err)
	}
	// Read one record and print it.
	nbytes, err := conn.Read(buf)
	if err != nil {
		if err == io.EOF {
			log.Infof("Connection closed by server.")
		} else {
			log.Errorf("Error reading socket: %v", err)
		}
		return "", err
	}
	conn.Close()
	return string(buf[0:nbytes]), nil
}
