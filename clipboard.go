// clipboard.go - Clipboard related data structures and functions.
package main

import (
	"os/exec"
	"sync"

	log "github.com/sirupsen/logrus"
)

type clipboard struct {
	sync.RWMutex
	value string
}

func (x *clipboard) set(value string) {
	x.Lock()
	x.value = value
	x.Unlock()
}

func (x *clipboard) get() string {
	x.Lock()
	v := x.value
	x.Unlock()
	return v
}

// readClipboard returns the contents of the primary clipboard.
func readClipboard() string {
	// xclip will return an error on an empty clipboard, but
	// there's no portable way to fetch the return code. Being
	// that the case, we'll just ignore those (TODO: Fix this).
	xclip := exec.Command("xclip", "-o")
	out, err := xclip.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// writeClipboard sets the contents of the primary clipboard.
func writeClipboard(contents string) error {
	xclip := exec.Command("xclip")
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

	log.Debugf("writeClipboard: Set clipboard to %s", contents)
	return nil
}
