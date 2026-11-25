package follow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

const (
	// patience is how long an origin may take to answer, or to send the next byte, before castor gives up on a read.
	patience = 15 * time.Second

	// segmentLimit is far above any real segment or byte range, and far below a film served in its place.
	segmentLimit = 256 << 20
)

var errIdle = errors.New("the origin sent nothing for too long")

// read fetches a segment whole, then decrypts and unwraps its media payload.
func (f *feed) read(ctx context.Context, s timeline.Segment) ([]byte, error) {
	b, err := f.pull(ctx, s.URI, s.Range, segmentLimit)
	if err != nil {
		return b, err
	}
	body, wrapped, err := f.decoder.Segment(ctx, s, b, f.keyBytes)
	if wrapped != 0 {
		f.unwrapped.Or(uint32(wrapped))
	}
	return body, err
}

func (f *feed) initBytes(ctx context.Context, m timeline.Map) ([]byte, error) {
	return f.initsHeld.get(ctx, f.life, m, func(ctx context.Context) ([]byte, error) {
		b, err := f.pull(ctx, m.URI, m.Range, source.DocumentLimit)
		if err != nil {
			return b, err
		}
		return f.decoder.Map(ctx, m, b, f.keyBytes)
	})
}

// keyBytes relays a key that the downstream reader handles itself.
func (f *feed) keyBytes(ctx context.Context, uri string) ([]byte, error) {
	return f.keysHeld.get(ctx, f.life, uri, func(ctx context.Context) ([]byte, error) {
		return f.pull(ctx, uri, timeline.Range{}, source.DocumentLimit)
	})
}

// pull reads a resource whole, giving up once the origin goes quiet for patience, and refusing more than limit bytes.
func (f *feed) pull(ctx context.Context, uri string, r timeline.Range, limit int) ([]byte, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	quiet := time.AfterFunc(patience, func() { cancel(errIdle) })
	defer quiet.Stop()
	body, err := f.source.Read(ctx, uri, r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, err
	}
	defer func() { _ = body.Close() }()
	var out []byte
	chunk := make([]byte, 32<<10)
	for {
		n, err := body.Read(chunk)
		quiet.Reset(patience)
		if out = append(out, chunk[:n]...); len(out) > limit {
			return nil, fmt.Errorf("larger than %d bytes", limit)
		}
		switch {
		case err == io.EOF:
			return out, nil
		case err != nil && ctx.Err() != nil:
			return nil, context.Cause(ctx)
		case err != nil:
			return nil, err
		}
	}
}
