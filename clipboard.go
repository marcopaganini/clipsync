// clipboard.go - Clipboard related data structures and functions.
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

type clipboard struct {
	sync.RWMutex
	primary   string
	clipboard string
}

func (x *clipboard) setPrimary(value string) {
	x.Lock()
	x.primary = value
	x.Unlock()
}

func (x *clipboard) setClipboard(value string) {
	x.Lock()
	x.clipboard = value
	x.Unlock()
}

func (x *clipboard) getPrimary() string {
	x.Lock()
	v := x.primary
	x.Unlock()
	return v
}
func (x *clipboard) getClipboard() string {
	x.Lock()
	v := x.clipboard
	x.Unlock()
	return v
}

// readClipboard returns the contents of the chosen X selection.
func readClipboard(sel string) string {
	// xclip will return an error on an empty clipboard, but
	// there's no portable way to fetch the return code. Being
	// that the case, we'll just ignore those (TODO: Fix this).
	xclip := exec.Command("xclip", "-selection", sel, "-o")
	out, err := xclip.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// writeClipboard sets the contents of the chosen X selection.
func writeClipboard(contents string, sel string) error {
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

	log.Debugf("writeClipboard: Set clipboard(%s) to %s", sel, contents)
	return nil
}
