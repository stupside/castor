// Package spool provides an append-only on-disk buffer with blocking tails.
//
// A Spool decouples a producer from its consumers: the producer appends as
// fast as it can, and each Tail reads from byte 0, blocking at end-of-data
// until more bytes arrive instead of reporting EOF. Once the producer
// finishes, consumers drain the remainder and see EOF (or the producer's
// terminal error).
package spool

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
)

// Spool is the append-only buffer. Create one with New, feed it via Write,
// and finish with CloseWrite.
type Spool struct {
	path string
	w    *os.File

	mu     sync.Mutex
	cond   *sync.Cond
	size   int64
	closed bool  // no more writes are coming
	err    error // terminal write-side error, if any
}

func New(path string) (*Spool, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("creating spool file: %w", err)
	}
	s := &Spool{path: path, w: f}
	s.cond = sync.NewCond(&s.mu)
	return s, nil
}

// Write appends to the spool and wakes any blocked tails.
func (s *Spool) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	s.mu.Lock()
	s.size += int64(n)
	s.cond.Broadcast()
	s.mu.Unlock()
	return n, err
}

// CloseWrite marks the write side finished. err records why the producer
// stopped early (nil for a clean end); tails drain the remaining bytes and
// then see EOF (or the error).
func (s *Spool) CloseWrite(err error) {
	s.mu.Lock()
	s.closed = true
	s.err = err
	s.cond.Broadcast()
	s.mu.Unlock()
	_ = s.w.Close()
}

func (s *Spool) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

// Path returns the backing file path. A reader (e.g. ffprobe) may open it
// concurrently with the producer: the spool is append-only, so a read sees a
// consistent prefix of whatever has been written so far.
func (s *Spool) Path() string { return s.path }

// Remove deletes the backing file. Call after all tails are finished.
func (s *Spool) Remove() error {
	return os.Remove(s.path)
}

// Tail returns a reader over the spool from byte 0 that blocks at
// end-of-data until the writer appends more or closes. The reader also
// unblocks (with ctx.Err()) when ctx is cancelled — required because
// os/exec waits for stdin-feeding goroutines, which would otherwise hang
// on a parked Tail after the consumer process dies.
func (s *Spool) Tail(ctx context.Context) (io.ReadCloser, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, fmt.Errorf("opening spool for tail: %w", err)
	}
	t := &tailReader{spool: s, f: f, ctx: ctx}
	// Wake the cond loop when ctx dies so Read can observe cancellation.
	context.AfterFunc(ctx, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	return t, nil
}

type tailReader struct {
	spool  *Spool
	f      *os.File
	ctx    context.Context
	offset int64
}

func (t *tailReader) Read(p []byte) (int, error) {
	s := t.spool
	s.mu.Lock()
	for t.offset >= s.size && !s.closed && t.ctx.Err() == nil {
		s.cond.Wait()
	}
	size, closed, werr := s.size, s.closed, s.err
	s.mu.Unlock()

	if err := t.ctx.Err(); err != nil {
		return 0, err
	}
	if t.offset >= size {
		// closed and fully drained
		if werr != nil {
			return 0, fmt.Errorf("spool producer failed: %w", werr)
		}
		if closed {
			return 0, io.EOF
		}
	}

	n, err := t.f.ReadAt(p, t.offset)
	t.offset += int64(n)
	if err == io.EOF {
		// More data may arrive; the next Read blocks on the cond again.
		err = nil
		if n == 0 {
			return t.Read(p)
		}
	}
	return n, err
}

func (t *tailReader) Close() error {
	return t.f.Close()
}
