package progressive

import (
	"context"
	"io"
)

// tailAt reads the current run from offset, across into its successor.
func (s *Stream) tailAt(ctx context.Context, offset int64) (*joinedTail, error) {
	r := s.run.Load()
	t := &joinedTail{s: s, run: r, ctx: ctx, pos: offset}
	if r.next != nil && offset >= r.next.at {
		t.switched = true
		cur, err := r.next.spool.TailAt(ctx, offset-r.next.at)
		t.cur = cur
		return t, err
	}
	cur, err := r.spool.TailAt(ctx, offset)
	t.cur = cur
	return t, err
}

// joinedTail fails with errRestarted once a seek replaces its run.
type joinedTail struct {
	s        *Stream
	run      *run
	ctx      context.Context
	cur      io.ReadCloser
	pos      int64
	switched bool
}

func (t *joinedTail) current() (*run, bool) {
	r := t.s.run.Load()
	return r, r.gen == t.run.gen
}

func (t *joinedTail) Read(p []byte) (int, error) {
	r, ok := t.current()
	if !ok {
		return 0, errRestarted
	}
	if next := r.next; next != nil && !t.switched {
		if t.pos >= next.at {
			if err := t.carryOn(next); err != nil {
				return 0, err
			}
		} else {
			p = p[:min(int64(len(p)), next.at-t.pos)]
		}
	}
	n, err := t.cur.Read(p)
	t.pos += int64(n)
	if _, ok := t.current(); !ok && err != nil {
		return n, errRestarted
	}
	return n, err
}

func (t *joinedTail) carryOn(next *successor) error {
	_ = t.cur.Close()
	cur, err := next.spool.TailAt(t.ctx, t.pos-next.at)
	t.cur, t.switched = cur, true
	return err
}

func (t *joinedTail) Close() error {
	if t.cur == nil {
		return nil
	}
	return t.cur.Close()
}
