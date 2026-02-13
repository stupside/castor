package transcode

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

// errDataOverwritten is returned when a reader has fallen behind and its
// requested offset has been overwritten by newer data.
var errDataOverwritten = errors.New("requested data has been overwritten")

// ringBuffer is a fixed-capacity circular buffer that supports one writer
// and multiple concurrent readers. Data is addressed by a monotonically
// increasing byte offset; once the buffer wraps, the oldest data is
// silently overwritten.
type ringBuffer struct {
	writePos atomic.Int64 // monotonic total bytes written
	done     atomic.Bool  // writer has closed

	mu  sync.RWMutex // protects buf + err
	buf []byte
	cap int64
	err error // set once before done, read under RLock

	wakeMu sync.Mutex    // serialises broadcast
	wakeCh chan struct{} // closed-and-replaced to wake waiters
}

// newRingBuffer creates a ring buffer with the given capacity in bytes.
func newRingBuffer(capacity int) *ringBuffer {
	rb := &ringBuffer{
		buf:    make([]byte, capacity),
		cap:    int64(capacity),
		wakeCh: make(chan struct{}),
	}
	return rb
}

// broadcast wakes all goroutines waiting on the wake channel by closing the
// current channel and replacing it with a fresh one.
func (rb *ringBuffer) broadcast() {
	rb.wakeMu.Lock()
	ch := rb.wakeCh
	rb.wakeCh = make(chan struct{})
	rb.wakeMu.Unlock()
	close(ch)
}

// waiter returns the current wake channel. Callers must obtain this channel
// before checking conditions to avoid TOCTOU races with broadcast.
func (rb *ringBuffer) waiter() <-chan struct{} {
	rb.wakeMu.Lock()
	ch := rb.wakeCh
	rb.wakeMu.Unlock()
	return ch
}

// Write appends p to the ring buffer, overwriting the oldest data if
// necessary. Returns the original len(p) and nil.
func (rb *ringBuffer) Write(p []byte) (int, error) {
	total := len(p)
	rb.mu.Lock()
	for len(p) > 0 {
		start := rb.writePos.Load() % rb.cap
		n := copy(rb.buf[start:], p)
		rb.writePos.Add(int64(n))
		p = p[n:]
	}
	rb.mu.Unlock()
	rb.broadcast()
	return total, nil
}

// Close marks the buffer as done. Subsequent reads that have consumed all
// available data will receive io.EOF (or closeErr if non-nil).
func (rb *ringBuffer) Close(closeErr error) {
	rb.mu.Lock()
	if closeErr != nil {
		rb.err = closeErr
	}
	rb.mu.Unlock()
	rb.done.Store(true)
	rb.broadcast()
}

// copyFrom copies data starting at offset into p under a read lock.
// It re-validates writePos under the lock to catch wraparound overwrites.
func (rb *ringBuffer) copyFrom(p []byte, offset int64) (int, error) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	wp := rb.writePos.Load()

	oldest := max(wp-rb.cap, 0)
	if offset < oldest {
		return 0, errDataOverwritten
	}

	avail := min(wp-offset, int64(len(p)), rb.cap)

	var n int
	start := offset % rb.cap
	if start+avail <= rb.cap {
		n = copy(p, rb.buf[start:start+avail])
	} else {
		first := rb.cap - start
		n = copy(p, rb.buf[start:])
		n += copy(p[first:], rb.buf[:avail-first])
	}

	return n, nil
}

// ReadAt copies data starting at the monotonic byte offset into p.
// It blocks until data is available, the buffer is closed, or ctx is
// cancelled. Returns ErrDataOverwritten if offset has already been
// overwritten.
func (rb *ringBuffer) ReadAt(ctx context.Context, p []byte, offset int64) (int, error) {
	for {
		wp := rb.writePos.Load()

		oldest := max(wp-rb.cap, 0)
		if offset < oldest {
			return 0, errDataOverwritten
		}

		if wp > offset {
			return rb.copyFrom(p, offset)
		}

		if rb.done.Load() {
			rb.mu.RLock()
			err := rb.err
			rb.mu.RUnlock()
			if err != nil {
				return 0, err
			}
			return 0, io.EOF
		}

		ch := rb.waiter()

		wp = rb.writePos.Load()
		if wp > offset {
			return rb.copyFrom(p, offset)
		}
		if rb.done.Load() {
			rb.mu.RLock()
			err := rb.err
			rb.mu.RUnlock()
			if err != nil {
				return 0, err
			}
			return 0, io.EOF
		}

		select {
		case <-ch:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// OldestOffset returns the lowest byte offset still available in the buffer.
func (rb *ringBuffer) OldestOffset() int64 {
	wp := rb.writePos.Load()
	return max(wp-rb.cap, 0)
}

// WaitForData blocks until the buffer contains at least minOffset total
// bytes written, the buffer is closed, or ctx is cancelled.
func (rb *ringBuffer) WaitForData(ctx context.Context, minOffset int64) error {
	for {
		if rb.writePos.Load() >= minOffset {
			return nil
		}

		if rb.done.Load() {
			return nil
		}

		ch := rb.waiter()

		if rb.writePos.Load() >= minOffset || rb.done.Load() {
			return nil
		}

		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
