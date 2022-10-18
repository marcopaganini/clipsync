// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"fmt"
	"strconv"
	"strings"
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
