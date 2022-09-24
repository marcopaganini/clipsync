// selection.go - Clipboard related data structures and functions.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.
package main

import (
	"os/exec"
	"sync"

	log "github.com/sirupsen/logrus"
)

// Clipboard Selection Types.
const (
	selPrimary   = "primary"
	selClipboard = "clipboard"
)

// selection contains a representation of the clipboard in memory
// with methods to allow atomic reads and writes.
type selection struct {
	sync.RWMutex
	primary   string
	clipboard string
}

func (x *selection) setPrimary(value string) {
	x.Lock()
	x.primary = value
	x.Unlock()
}

func (x *selection) setClipboard(value string) {
	x.Lock()
	x.clipboard = value
	x.Unlock()
}

func (x *selection) getPrimary() string {
	x.Lock()
	v := x.primary
	x.Unlock()
	return v
}
func (x *selection) getClipboard() string {
	x.Lock()
	v := x.clipboard
	x.Unlock()
	return v
}

// getXSelection returns the contents of the chosen X selection.
func getXSelection(sel, mimetype string) string {
	// xclip will return an error on an empty clipboard, but
	// there's no portable way to fetch the return code. Being
	// that the case, we'll just ignore those (TODO: Fix this).
	args := []string{"-selection", sel, "-o"}
	if mimetype != "" {
		args = append(args, "-t", mimetype)
	}
	xclip := exec.Command("xclip", args...)
	out, err := xclip.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// setXSelection sets the contents of the chosen X selection.
func setXSelection(sel string, contents string) error {
	xclip := exec.Command("xclip", "-selection", sel, "-i")
	stdin, err := xclip.StdinPipe()
	if err != nil {
		return err
	}
	xclip.Start()

	if _, err = stdin.Write([]byte(contents)); err != nil {
		return err
	}
	stdin.Close()
	xclip.Wait()

	log.Debugf("Set selection(%s) to: %s", sel, redact(contents))
	return nil
}

// Syntactic sugar functions to access the X clipboard.

func setXClipboard(contents string) error {
	return setXSelection(selClipboard, contents)
}

func setXPrimary(contents string) error {
	return setXSelection(selPrimary, contents)
}

func getXPrimary(mimetype string) string {
	return getXSelection(selPrimary, mimetype)
}

func getXClipboard(mimetype string) string {
	return getXSelection(selClipboard, mimetype)
}
