// util.go - Miscellaneous function of general use.
//
// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"fmt"
	"regexp"
)

const redactLen = 50

// redact returns a shortened and partially redacted string.
func redact(s string) string {
	// This is somewhat inefficient.
	re := regexp.MustCompile("[[:cntrl:]]")
	s = re.ReplaceAllString(s, ".")
	ret := fmt.Sprintf("[%q]", s)

	// Only redact if too long.
	if len(s) > redactLen {
		ret = fmt.Sprintf("[%s(...)%s]", s[:redactLen/2], s[len(s)-redactLen/2:])
	}
	ret += fmt.Sprintf(" length=%d", len(s))
	return ret
}
