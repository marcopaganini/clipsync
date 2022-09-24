// util.go - Miscellaneous function of general use.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import "fmt"

const redactLen = 50

// redact returns a shortened and partially redacted string.
func redact(s string) string {
	ret := fmt.Sprintf("[%s]", s)

	// Only redact if too long.
	if len(s) > redactLen {
		ret = fmt.Sprintf("[%s<==REDACTED==>%s]", s[:redactLen/2], s[redactLen/2:])
	}
	ret += fmt.Sprintf(" length=%d", len(s))
	return ret
}
