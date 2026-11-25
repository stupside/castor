// Package timeline is how a segmented origin lists its media: the segments a format reads from it, and the source that keeps reading them.
package timeline

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Segment is one piece of media as the origin lists it, every reference absolute.
type Segment struct {
	URI      string
	Duration time.Duration
	Range    Range
	Map      *Map
	Key      Key
	// Seam is a break the origin itself declares before this segment.
	Seam bool
	// Place is where the origin puts the segment on its own clock.
	Place Place
}

// Range is a byte range of a resource; a zero Length is the whole resource.
type Range struct{ Offset, Length int64 }

// Header is the Range request header that asks for r, which must have a length.
func (r Range) Header() string { return fmt.Sprintf("bytes=%d-%d", r.Offset, r.Offset+r.Length-1) }

// Map is the initialization section a segment is decoded with, encrypted under Key when one was in force.
type Map struct {
	URI   string
	Range Range
	Key   Key
}

// Key is how a segment is encrypted; the zero Key is clear.
type Key struct {
	Method, URI, IV, Format string
}

// IdentityFormat is the KEYFORMAT of a key whose URI serves the raw key bytes, as opposed to a DRM system's.
const IdentityFormat = "identity"

// Place names the run of numbering a segment belongs to and the span it covers, End being where the next begins.
type Place struct {
	Period     string
	Start, End int64
}

// Window is what the origin lists now.
type Window struct {
	Segments []Segment
	// Closed is an origin that will publish nothing more.
	Closed bool
	Start  *Start
}

// Start is where the origin asks playback to begin, from the first segment or, when negative, from the end.
type Start struct {
	Offset  time.Duration
	Precise bool
}

// Source yields the origin's current window each time it is asked, never twice at once, and reads the media it lists.
type Source interface {
	Window(ctx context.Context) (Window, error)
	// Read opens a resource the window names, as the input's own reader would ask for it.
	Read(ctx context.Context, uri string, r Range) (io.ReadCloser, error)
}

// KeyReader retrieves a key through the same bounded, cached origin session as media.
type KeyReader func(context.Context, string) ([]byte, error)

// Decoder handles the origin format's resource encoding. Implementations must be safe
// for concurrent segment reads. Segment reports the source envelope it removed.
type Decoder interface {
	Segment(context.Context, Segment, []byte, KeyReader) ([]byte, media.HLSExtraSegmentFormat, error)
	Map(context.Context, Map, []byte, KeyReader) ([]byte, error)
	ConsumesKey(Key) bool
}

// Reading is a format's contract with the shared timeline relay. A followed
// source needs a decoder. DirectFallback explicitly permits FFmpeg to read the
// origin URL if Castor cannot open the first window.
type Reading struct {
	Source         Source
	Decoder        Decoder
	DirectFallback bool
}

// IdentityDecoder passes resources through when a format has no resource envelope.
type IdentityDecoder struct{}

func (IdentityDecoder) Segment(_ context.Context, _ Segment, body []byte, _ KeyReader) ([]byte, media.HLSExtraSegmentFormat, error) {
	return body, 0, nil
}

func (IdentityDecoder) Map(_ context.Context, _ Map, body []byte, _ KeyReader) ([]byte, error) {
	return body, nil
}

func (IdentityDecoder) ConsumesKey(Key) bool { return false }

// Failure is an origin that answered a window request with a status.
type Failure struct {
	Status int
	Err    error
}

func (f *Failure) Error() string { return fmt.Sprintf("origin answered %d: %v", f.Status, f.Err) }

func (f *Failure) Unwrap() error { return f.Err }
