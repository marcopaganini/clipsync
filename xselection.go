// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// Clipboard Selection Types.
	selPrimary   = "primary"
	selClipboard = "clipboard"
	// Timeout when running xclip, in ms.
	xclipTimeout = 1500
)

// client contains a representation of a MQTT client.
type xselection struct {
	sync.RWMutex
	primary   string
	clipboard string
}

func (x *xselection) setMemPrimary(value string) {
	x.Lock()
	x.primary = value
	x.Unlock()
}

func (x *xselection) setMemClipboard(value string) {
	x.Lock()
	x.clipboard = value
	x.Unlock()
}

func (x *xselection) getMemPrimary() string {
	x.Lock()
	v := x.primary
	x.Unlock()
	return v
}
func (x *xselection) getMemClipboard() string {
	x.Lock()
	v := x.clipboard
	x.Unlock()
	return v
}

// getXSelection returns the contents of the chosen X selection.
func (x *xselection) getXSelection(sel, mimetype string) string {
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
		// Don't log anything here, as running xclip on an empty clipboard will
		// return an error. This is a common and harmless occurrence.
		return ""
	}
	return string(out)
}

// setXSelection sets the contents of the chosen X selection.
func (x *xselection) setXSelection(sel string, contents string) error {
	x.Lock()
	defer x.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), xclipTimeout*time.Millisecond)
	defer cancel()

	xclip := exec.CommandContext(ctx, "xclip", "-selection", sel, "-i")
	stdin, err := xclip.StdinPipe()
	if err != nil {
		return fmt.Errorf("Error reading xclip stdin: %v", err)
	}
	if err := xclip.Start(); err != nil {
		return fmt.Errorf("Error starting xclip: %v", err)
	}

	if _, err = stdin.Write([]byte(contents)); err != nil {
		return err
	}
	stdin.Close()
	if err = xclip.Wait(); err != nil {
		return fmt.Errorf("Error waiting for xclip: %v", err)
	}

	log.Debugf("Set selection(%s) to: %s", sel, redact.redact(contents))
	return nil
}

// Syntactic sugar functions to access the X clipboard.

func (x *xselection) setXClipboard(contents string) error {
	return x.setXSelection(selClipboard, contents)
}

func (x *xselection) setXPrimary(contents string) error {
	return x.setXSelection(selPrimary, contents)
}

func (x *xselection) getXPrimary(mimetype string) string {
	return x.getXSelection(selPrimary, mimetype)
}

func (x *xselection) getXClipboard(mimetype string) string {
	return x.getXSelection(selClipboard, mimetype)
}
