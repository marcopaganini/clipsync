// clipboard.go - Clipboard related data structures.
package main

import "sync"

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
