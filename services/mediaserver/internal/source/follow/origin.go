package follow

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// read opens a segment, decrypted when the origin encrypted it whole.
func (f *feed) read(ctx context.Context, s timeline.Segment) (io.ReadCloser, error) {
	if !decryptable(s.Key) {
		return f.open(ctx, s.URI, s.Range)
	}
	sealed, err := f.whole(ctx, s.URI, s.Range)
	if err != nil {
		return nil, err
	}
	clear, err := f.decrypt(ctx, sealed, s.Key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(clear)), nil
}

func (f *feed) initBytes(ctx context.Context, m timeline.Map) ([]byte, error) {
	return f.initsHeld.get(ctx, m, func(ctx context.Context) ([]byte, error) {
		b, err := f.document(ctx, m.URI, m.Range)
		if err != nil || !decryptable(m.Key) {
			return b, err
		}
		return f.decrypt(ctx, b, m.Key)
	})
}

// keyBytes relays a key that the downstream reader handles itself.
func (f *feed) keyBytes(ctx context.Context, uri string) ([]byte, error) {
	return f.keysHeld.get(ctx, uri, func(ctx context.Context) ([]byte, error) { return f.document(ctx, uri, timeline.Range{}) })
}

// decrypt fetches the raw AES key through the segment's origin session.
func (f *feed) decrypt(ctx context.Context, sealed []byte, key timeline.Key) ([]byte, error) {
	secret, err := f.keysHeld.get(ctx, key.URI, func(ctx context.Context) ([]byte, error) {
		body, err := f.open(ctx, key.URI, timeline.Range{})
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return readAESKey(body)
	})
	if err != nil {
		return nil, fmt.Errorf("reading the key: %w", err)
	}
	return decryptAES128(sealed, secret, key.IV)
}

// whole reads a resource to its end.
func (f *feed) whole(ctx context.Context, uri string, r timeline.Range) ([]byte, error) {
	body, err := f.open(ctx, uri, r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return io.ReadAll(body)
}

// document reads a resource the feed holds on to, refusing one larger than any init section or key.
func (f *feed) document(ctx context.Context, uri string, r timeline.Range) ([]byte, error) {
	body, err := f.open(ctx, uri, r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return source.ReadDocument(body)
}

// open reads from the origin with patience to answer, and patience again whenever it goes quiet.
func (f *feed) open(ctx context.Context, uri string, r timeline.Range) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	quiet := time.AfterFunc(f.patience, cancel)
	body, err := f.source.Read(ctx, uri, r)
	if err != nil {
		quiet.Stop()
		cancel()
		return nil, err
	}
	return &patient{ReadCloser: body, quiet: quiet, patience: f.patience, cancel: cancel}, nil
}

// patient is an origin read that gives up once no byte has arrived for its patience.
type patient struct {
	io.ReadCloser
	quiet    *time.Timer
	patience time.Duration
	cancel   context.CancelFunc
}

func (p *patient) Read(b []byte) (int, error) {
	n, err := p.ReadCloser.Read(b)
	p.quiet.Reset(p.patience)
	return n, err
}

func (p *patient) Close() error {
	p.quiet.Stop()
	p.cancel()
	return p.ReadCloser.Close()
}
