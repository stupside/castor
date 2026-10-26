package execute

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// Machinery is what every attempt of every cast runs on.
type Machinery struct {
	// Outputs are the delivery adapters installed by the media server.
	Outputs output.Factory

	// FFmpegPath is the binary every read and encode runs, and Binary what it understands.
	FFmpegPath string
	Binary     ffmpeg.Binary

	// Encoders is the host's encoder lookup, bound to that binary.
	Encoders codec.Encoders

	// Probes measures what an attempt reads: its source program, or its local buffer.
	Probes probe.FFprobe

	// InputArgs is how ffmpeg opens an input, as its source format says.
	InputArgs encoding.InputArgs
}

// Cast is what one cast's attempts run with, on the machinery every cast shares.
type Cast struct {
	Machinery

	Device Device

	Publisher output.Publisher

	// Captions is nil for a cast that wants none of the source's own.
	Captions *Captions

	// Timelines uses this cast's source session across its recovery attempts.
	Timelines Timelines

	// Transcription selects an attempt's lazy recognizer after audio resolution.
	// Nil disables generated subtitles.
	Transcription func(ctx context.Context, audioLanguage string) Subtitles

	// MaxHeight is the tallest picture the cast may show.
	MaxHeight media.HeightCap

	// Resolver reads the other rungs of a source, which a cast moves between while it plays; nil keeps it on its first.
	Resolver recovery.SourceResolver

	// MaxBitrate is the most the cast may ask of the device's link, zero for none.
	MaxBitrate media.Bitrate
}

// Device is what a cast plays on, connected for the whole cast by whoever lent it, who also closes it.
type Device interface {
	Play(ctx context.Context, p Playback) error

	AwaitEnd(ctx context.Context) error

	// Pause holds the device on its picture until Resume, while the read behind it gets ahead again.
	Pause(context.Context) error
	Resume(context.Context) error

	Capabilities() media.Capabilities
}

// Playback is what a device is pointed at: the stream at URL, and the captions shown over it.
type Playback struct {
	URL      *url.URL
	Captions []media.Caption
	Stream   Stream
}

// Stream is how what the device plays reaches it: a Source, a Served stream or a Playlist.
type Stream interface{ stream() }

// Source is the source itself, fetched from its own server, which alone knows how it seeks.
type Source struct {
	ContentType string
	// Runtime is zero where the source declared none.
	Runtime time.Duration
}

// Served is one stream castor serves, from its start only where it has no Runtime.
type Served struct {
	ContentType string
	Runtime     *Runtime
}

// Runtime is a served stream's length from where it begins; TimeSeek serves it from any time within.
type Runtime struct {
	Length   time.Duration
	TimeSeek bool
}

// Playlist is an HLS playlist castor serves, which the device seeks within by its segments.
type Playlist struct {
	// Runtime is zero where the source declared none.
	Runtime time.Duration
}

func (Source) stream()   {}
func (Served) stream()   {}
func (Playlist) stream() {}

func (s Source) LogValue() slog.Value {
	return slog.GroupValue(slog.String("kind", "source"), slog.String("content_type", s.ContentType), slog.Duration("runtime", s.Runtime))
}

func (s Served) LogValue() slog.Value {
	attrs := []slog.Attr{slog.String("kind", "served"), slog.String("content_type", s.ContentType)}
	if s.Runtime != nil {
		attrs = append(attrs, slog.Duration("runtime", s.Runtime.Length), slog.Bool("time_seek", s.Runtime.TimeSeek))
	}
	return slog.GroupValue(attrs...)
}

func (p Playlist) LogValue() slog.Value {
	return slog.GroupValue(slog.String("kind", "playlist"), slog.Duration("runtime", p.Runtime))
}

// Subtitles opens the transcription chosen for this attempt, independently of
// how its cues are rendered. Source and language selection happened upstream.
type Subtitles func(ctx context.Context) Transcript

// Transcript owns recognition and exposes committed timed cues to the selected renderer.
type Transcript interface {
	health.Lead
	SampleRate() int
	Run(ctx context.Context, pcm io.ReadCloser)
	Cues() *subtitle.Cues
}

// burnRenderer draws committed cues into video for receivers without native delivery.
type burnRenderer interface {
	Create() (string, error)
	Writer(ctx context.Context) func(media.Progress)
}

// Timelines republishes a program's followed inputs for one read. The returned
// function joins serving and reports its final outcome, even if the reader succeeded.
type Timelines interface {
	Republish(ctx context.Context, program media.Program) (media.Program, func() error, error)
}
