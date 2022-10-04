// util.go - Miscellaneous function of general use.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"fmt"
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
	// Default response.
	ret := fmt.Sprintf("[%q]", s)

	// Only redact if too long.
	if x.maxlen <= 0 {
		x.maxlen = redactDefaultLen
	}
	if len(s) > x.maxlen {
		ret = fmt.Sprintf("[%q(...)%q]", s[:x.maxlen/2], s[len(s)-x.maxlen/2:])
	}
	ret += fmt.Sprintf(" length=%d", len(s))
	return ret
}
