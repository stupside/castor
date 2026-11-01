// Package awake keeps the machine from idling to sleep while castor casts; the screen may still sleep and the user may still put the machine to sleep.
package awake

import (
	"log/slog"
	"sync"
)

var (
	mu      sync.Mutex
	holds   int
	release func()
)

// Hold keeps the machine awake until the returned func is called; every hold in the process shares one request to the OS.
func Hold() func() {
	mu.Lock()
	defer mu.Unlock()
	holds++
	if holds == 1 {
		release = acquire()
	}
	return sync.OnceFunc(func() {
		mu.Lock()
		defer mu.Unlock()
		holds--
		if holds == 0 {
			release()
		}
	})
}

// acquire asks the OS to keep the machine awake, or holds nothing where it cannot.
func acquire() func() {
	release, err := inhibit()
	if err != nil {
		slog.Debug("the machine may idle to sleep", "err", err)
		return func() {}
	}
	return release
}
