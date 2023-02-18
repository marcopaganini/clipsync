// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fredli74/lockfile"
	log "github.com/romana/rlog"
)

// Show at most this number of characters on a redacted string
// (half at the beginning, half at the end.)
const redactDefaultLen = 50

// redact holds the level of redaction.
type redactType struct {
	maxlen int
}

// redact returns a shortened and partially redacted string.
func (x redactType) redact(s string) string {
	ret := fmt.Sprintf("[%s]", strquote(s))

	// Only redact if too long.
	if x.maxlen <= 0 {
		x.maxlen = redactDefaultLen
	}
	if len(s) > x.maxlen {
		ret = fmt.Sprintf("[%s(...)%s]", strquote(s[:x.maxlen/2]), strquote(s[len(s)-x.maxlen/2:]))
	}
	ret += fmt.Sprintf(" length=%d", len(s))
	return ret
}

// strquote returns a quoted string, but removes the external quotes and
// replaces \" for " inside the string.
func strquote(s string) string {
	ret := strings.ReplaceAll(strconv.Quote(s), `\"`, `"`)
	return ret[1 : len(ret)-1]
}

// singleInstanceOrDie guarantees that this is the only instance of
// this program using the specified lockfile. Caller must call
// Unlock on the returned lock once it's not needed anymore.
func singleInstanceOrDie(lckfile string) *lockfile.LockFile {
	lock, err := lockfile.Lock(lckfile)
	if err != nil {
		fatalf("Another instance is already running.")
	}
	return lock
}

// tildeExpand expands the tilde at the beginning of a filename to $HOME.
func tildeExpand(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	dirname, err := os.UserHomeDir()
	if err != nil {
		log.Errorf("Unable to locate homedir when expanding: %q", path)
		return path
	}
	return filepath.Join(dirname, path[2:])
}

// fileExists if the given file exists and is a file (not a directory).
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
