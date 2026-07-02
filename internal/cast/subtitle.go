package cast

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/cast/whisper"
)

const (
	// defaultSubtitleFont ships with every macOS install.
	defaultSubtitleFont = "/System/Library/Fonts/Helvetica.ttc"

	// cueLeadBias compensates for the encoder pipeline running ahead of the
	// mux position that -progress reports: frames pass through drawtext
	// roughly an encoder-lookahead before they are muxed, so we look up the
	// cue slightly ahead of out_time. Whisper timing itself is only ~±0.5s
	// accurate, so this doesn't need to be exact.
	cueLeadBias = 1.0

	// cueWrapColumns is where subtitle lines wrap. ~42 columns matches
	// broadcast subtitle conventions and keeps two lines inside the safe
	// area at the drawtext font size.
	cueWrapColumns = 42
)

// subtitles is the optional transcription stage: an in-process whisper model
// fed by the puller's PCM tee, whose cues are burned into the video by the
// encoder's drawtext filter via a live-swapped textfile.
type subtitles struct {
	tr      *whisper.Transcriber
	cuePath string
	font    string
}

// newSubtitles prepares the transcription stage when the plan asks for
// hardsubs. Whisper init failure downgrades to a subtitle-less cast (nil)
// rather than blocking playback.
func newSubtitles(ctx context.Context, cfg Config, plan Plan, workDir string) *subtitles {
	if plan.SubtitleDelivery != SubtitleHardsub {
		return nil
	}
	tr, err := whisper.New(ctx, cfg.Whisper)
	if err != nil {
		slog.WarnContext(ctx, "whisper init failed; casting without subtitles", "error", err)
		return nil
	}
	font := cfg.Transcode.SubtitleFontFile
	if font == "" {
		font = defaultSubtitleFont
	}
	return &subtitles{
		tr:      tr,
		cuePath: filepath.Join(workDir, "cue.txt"),
		font:    font,
	}
}

// transcribe consumes the PCM feed in g until EOF. If transcription fails,
// the feed keeps draining: PCM backpressure would otherwise stall the puller
// and starve the spool the encoder is playing from.
func (s *subtitles) transcribe(ctx context.Context, g *errgroup.Group, pcm io.ReadCloser) {
	g.Go(func() error {
		defer pcm.Close()
		if err := s.tr.Run(ctx, pcm); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "transcription failed; subtitles stop here", "error", err)
			_, _ = io.Copy(io.Discard, pcm)
		}
		return nil
	})
}

// attach wires the burn-in into the encoder options. The cue file must exist
// before ffmpeg starts or drawtext's filter init fails.
func (s *subtitles) attach(opts *ffmpeg.EncodeOptions) error {
	if err := os.WriteFile(s.cuePath, nil, 0o644); err != nil {
		return fmt.Errorf("creating subtitle cue file: %w", err)
	}
	opts.SubtitleTextFile = s.cuePath
	opts.SubtitleFontFile = s.font
	return nil
}

// follow runs the cue writer in g against the encoder's -progress feed,
// keeping the textfile holding the line for the frame currently being
// encoded. It returns when the feed ends (encoder exited).
func (s *subtitles) follow(ctx context.Context, g *errgroup.Group, progress io.Reader) {
	g.Go(func() error {
		s.runCueWriter(ctx, progress)
		return nil
	})
}

// runCueWriter follows the encoder's -progress feed and keeps the cue file
// holding the subtitle line for the frame currently being encoded. Updates
// are written to a temp file in the same directory and renamed into place:
// drawtext re-opens the path before every frame and a partially-written or
// missing file would kill ffmpeg, so atomic replacement is mandatory.
func (s *subtitles) runCueWriter(ctx context.Context, progress io.Reader) {
	tmpPath := s.cuePath + ".tmp"
	last := ""
	calls := 0
	wroteCue := false
	ffmpeg.WatchProgress(progress, func(seconds float64) {
		calls++
		lookup := seconds + cueLeadBias
		text := wrapCue(s.tr.CueAt(lookup), cueWrapColumns)
		// Surface the encoder position against how far transcription has
		// reached until the first cue lands, so a silent gap (encoder ahead
		// of the commit frontier, or out_time stuck) is visible in --debug.
		if !wroteCue && (calls == 1 || calls%50 == 0) {
			slog.DebugContext(ctx, "cue writer waiting",
				"calls", calls,
				"out_time", seconds,
				"lookup", lookup,
				"latest_end", s.tr.LatestEnd(),
				"empty", text == "",
			)
		}
		if text == last {
			return
		}
		if err := os.WriteFile(tmpPath, []byte(text), 0o644); err != nil {
			slog.WarnContext(ctx, "writing subtitle cue", "error", err)
			return
		}
		if err := os.Rename(tmpPath, s.cuePath); err != nil {
			slog.WarnContext(ctx, "swapping subtitle cue", "error", err)
			return
		}
		if !wroteCue && text != "" {
			slog.InfoContext(ctx, "first subtitle cue rendered", "out_time", seconds, "text", text)
			wroteCue = true
		}
		slog.DebugContext(ctx, "subtitle cue swapped", "out_time", seconds, "text", text)
		last = text
	})
}

// wrapCue normalizes whitespace and greedily wraps text at width columns so
// drawtext renders broadcast-style line breaks. Words longer than width are
// emitted as-is on their own line.
func wrapCue(text string, width int) string {
	// FieldsSeq iterates the whitespace-split words without allocating the
	// intermediate slice; empty input simply yields no iterations, so the
	// builder stays empty and we return "".
	var b strings.Builder
	lineLen := 0
	for w := range strings.FieldsSeq(text) {
		switch {
		case lineLen == 0:
			b.WriteString(w)
			lineLen = len(w)
		case lineLen+1+len(w) <= width:
			b.WriteByte(' ')
			b.WriteString(w)
			lineLen += 1 + len(w)
		default:
			b.WriteByte('\n')
			b.WriteString(w)
			lineLen = len(w)
		}
	}
	return b.String()
}
