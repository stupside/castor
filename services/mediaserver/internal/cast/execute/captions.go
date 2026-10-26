package execute

import (
	"log/slog"
	"slices"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/captions"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Captions is how a cast shows the text the source published, nil for a cast that wants none.
type Captions struct {
	Languages []string
	Client    source.Client
}

// fetchedCaption is the source caption a cast shows, as the source wrote it.
type fetchedCaption struct {
	language, name string
	format         media.CaptionFormat
	vtt            string
}

// shownCaptions serves the fetched caption for the device, its cues moved to where the attempt's program starts.
// A caption is a courtesy: whatever stops it is logged and the cast plays without.
func (s *preparation) shownCaptions() []media.Caption {
	c := s.caption
	if c == nil {
		return nil
	}
	vtt := c.vtt
	if start := s.attempt.Program.Start; start > 0 {
		vtt = captions.Shifted(vtt, start)
	}
	name, contentType, body := "captions.vtt", "text/vtt", vtt
	if c.format == media.CaptionSRT {
		name, contentType, body = "captions.srt", "text/srt", captions.SRT(vtt)
	}
	u, closeStatic, err := output.Static(s.ctx, s.Publisher, name, contentType, body)
	if err != nil {
		slog.WarnContext(s.ctx, "casting without the source's captions", "error", err)
		return nil
	}
	s.releases.publication(closeStatic)
	slog.InfoContext(s.ctx, "showing the source's captions", "language", c.language, "format", string(c.format))
	return []media.Caption{{URL: u, Format: c.format, Language: c.language, Name: c.name, Default: true}}
}

// fetchCaption is the first caption in a language asked for that the source serves, nil where none.
func (s *preparation) fetchCaption() *fetchedCaption {
	want := s.Captions
	published := s.attempt.Rendition.Captions
	if want == nil || len(published) == 0 {
		return nil
	}
	format, ok := captionFormat(s.capabilities)
	if !ok {
		slog.InfoContext(s.ctx, "the source publishes captions, but the device shows none")
		return nil
	}
	matches := captions.Matches(published, want.Languages)
	if len(matches) == 0 {
		slog.InfoContext(s.ctx, "the source publishes no caption in the language asked", "published", len(published))
		return nil
	}
	primary, _ := s.attempt.Program.PrimaryInput()
	var origin *time.Duration
	if measured, ok := s.attempt.Program.Measurement(); ok && measured.StartKnown {
		origin = &measured.Start
	}
	for _, pick := range matches {
		vtt, err := captions.FetchAt(s.ctx, want.Client, pick, primary.Headers, origin)
		if err != nil {
			slog.WarnContext(s.ctx, "source caption unavailable, trying the next track", "language", pick.Language, "error", err)
			continue
		}
		return &fetchedCaption{language: pick.Language, name: pick.Name, format: format, vtt: vtt}
	}
	return nil
}

// captionFormat is the format a device takes captions in, WebVTT before SubRip.
func captionFormat(c media.Capabilities) (media.CaptionFormat, bool) {
	for _, f := range []media.CaptionFormat{media.CaptionVTT, media.CaptionSRT} {
		if slices.ContainsFunc(c.Formats, func(format media.FormatSupport) bool { return slices.Contains(format.Captions, f) }) {
			return f, true
		}
	}
	return "", false
}

// playbackCapabilities keeps sidecar support attached to the codec/transport
// envelope that advertised it. Both handoff and encoding consume this selection.
func (s *preparation) playbackCapabilities() media.Capabilities {
	caps := s.capabilities
	if s.caption == nil {
		return caps
	}
	caps.Formats = slices.DeleteFunc(slices.Clone(caps.Formats), func(f media.FormatSupport) bool {
		return !slices.Contains(f.Captions, s.caption.format)
	})
	return caps
}

// nativeOutput asks the installed delivery adapters to carry generated cues.
// Source clock availability is a media fact; protocol eligibility belongs to adapters.
func (s *preparation) nativeOutput(f feed) (output.Selection, bool) {
	if f.buffered == nil || f.buffered.transcript == nil || f.buffered.captionVideoOffset == nil {
		return output.Selection{}, false
	}
	selected, err := s.Outputs.Select(s.playbackCapabilities(), &output.Captions{
		Cues: f.buffered.transcript.Cues(), VideoOffset: *f.buffered.captionVideoOffset,
	})
	return selected, err == nil
}
