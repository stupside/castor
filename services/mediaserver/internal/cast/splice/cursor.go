package splice

import (
	"context"
	"io"
	"sync"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
)

// Cursor reads a buffer from an offset, to a limit that may be set while it reads.
type Cursor struct {
	tail io.ReadCloser

	// skip drops the sound a reader starting at a cut has already been given, nil for none.
	skip Trimmer

	mu     sync.Mutex
	prefix []byte
	// ready is what the skipper let through that a read has not taken yet.
	ready []byte

	// next is where the next byte comes from, counting the bytes a read in flight has claimed.
	next  int64
	limit int64
}

// Open reads sp from offset from, passing what it reads through skip unless nil. A cursor starting at a cut is given the tables it needs first.
func Open(ctx context.Context, sp *storage.Spool, from int64, tables []byte, skip Trimmer) (*Cursor, error) {
	tail, err := sp.TailAt(ctx, from)
	if err != nil {
		return nil, err
	}
	return &Cursor{tail: tail, skip: skip, prefix: tables, next: from, limit: -1}, nil
}

func (c *Cursor) Read(p []byte) (int, error) {
	if c.skip == nil {
		return c.read(p)
	}
	for {
		c.mu.Lock()
		if len(c.ready) > 0 {
			n := copy(p, c.ready)
			c.ready = c.ready[n:]
			c.mu.Unlock()
			return n, nil
		}
		c.mu.Unlock()
		n, err := c.read(p)
		kept := c.skip.Trim(p[:n])
		c.mu.Lock()
		c.ready = append(c.ready, kept...)
		c.mu.Unlock()
		if len(kept) == 0 && err != nil {
			return 0, err
		}
	}
}

func (c *Cursor) read(p []byte) (int, error) {
	c.mu.Lock()
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		c.mu.Unlock()
		return n, nil
	}
	claim := len(p)
	if c.limit >= 0 {
		claim = int(min(int64(claim), c.limit-c.next))
		if claim <= 0 {
			c.mu.Unlock()
			return 0, io.EOF
		}
	}
	c.next += int64(claim)
	c.mu.Unlock()

	n, err := c.tail.Read(p[:claim])

	c.mu.Lock()
	c.next -= int64(claim - n)
	c.mu.Unlock()
	return n, err
}

// Offset is how far into the buffer the cursor has read.
func (c *Cursor) Offset() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.next
}

// Stop ends the cursor's reading at offset at, reporting whether it had not read past it already.
func (c *Cursor) Stop(at int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if at < c.next {
		return false
	}
	c.limit = at
	return true
}

func (c *Cursor) Close() error { return c.tail.Close() }
