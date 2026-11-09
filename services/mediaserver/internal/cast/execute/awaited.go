package execute

import (
	"io"
	"sync/atomic"
)

// awaited is an encoder's output that knows whether castor is blocked reading it.
type awaited struct {
	io.Reader
	reading atomic.Bool
}

func (a *awaited) Read(p []byte) (int, error) {
	a.reading.Store(true)
	defer a.reading.Store(false)
	return a.Reader.Read(p)
}

func (a *awaited) waiting() bool { return a.reading.Load() }
