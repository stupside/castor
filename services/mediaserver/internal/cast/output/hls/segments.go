package hls

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Segments is a serving view over HLS artifacts. It starts no work and owns no
// publication or producer; its root is closed after the attempt joins its users.
type Segments struct {
	o           output.Opening
	store       *segmentStore
	served      atomic.Int64
	mu          sync.Mutex
	lastRequest time.Time
}

func newSegments(o output.Opening, dir string, captions *output.Captions) (*Segments, error) {
	store, err := openSegmentStore(dir, o.Format.Tuning.Output)
	if err != nil {
		return nil, err
	}
	if captions != nil {
		store.captions = &captionDelivery{root: store.root, playlist: o.Format.Tuning.Output, cues: captions.Cues, videoOffset: captions.VideoOffset, vtt: map[string]string{}}
	}
	return &Segments{o: o, store: store, lastRequest: time.Now()}, nil
}

// Write packages native captions, or drains FFmpeg's stdout when FFmpeg writes
// the HLS files itself. The caller owns the reader and its interruption.
func (s *Segments) Write(ctx context.Context, producer io.Reader) error {
	if s.store.captions != nil {
		return s.store.captions.write(ctx, producer)
	}
	_, err := io.Copy(io.Discard, producer)
	return err
}

func (s *Segments) Path() string {
	if s.store.captions != nil {
		return masterPlaylist
	}
	return s.o.Format.Tuning.Output
}
func (s *Segments) Served() int { return int(s.served.Load()) }
func (s *Segments) Artifact() output.Artifact {
	return output.Artifact{Path: s.store.playlist, Subject: "the HLS playlist", Landed: s.store.landed}
}
func (s *Segments) WatchStorage(ctx context.Context) error { return monitorDirectory(ctx, s.store.dir) }
func (s *Segments) Wait(ctx context.Context, produced <-chan struct{}) error {
	return output.Settle(ctx, produced, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return time.Since(s.lastRequest) > s.o.IdleGrace
	})
}
func (s *Segments) Close() error { return s.store.Close() }

func (*Segments) Audience() health.Audience { return nil }
func (s *Segments) Settled(made media.Progress) error {
	return health.NoneFetched(s.Served(), made)
}
