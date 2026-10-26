package execute

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

type textPages map[string]string

func (p textPages) Fetch(_ context.Context, u *url.URL, _ http.Header) (string, *url.URL, int, error) {
	return p[u.Path], u, http.StatusOK, nil
}
func (textPages) Replay(_ *url.URL, h http.Header) http.Header { return h }
func (textPages) Read(context.Context, *url.URL, http.Header, timeline.Range) (io.ReadCloser, error) {
	return nil, io.EOF
}

func captionedAttempt(t *testing.T, caps media.Capabilities, languages []string) *preparation {
	t.Helper()
	published := []source.Caption{{URL: &url.URL{Scheme: "http", Host: "origin.test", Path: "/subs/en.vtt"}, Language: "en", Name: "English"}}
	client := textPages{"/subs/en.vtt": "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhello\n"}
	s := testPreparation(t.Context(),
		Cast{Outputs: testOutputs(), Device: &fakeDevice{caps: caps}, Publisher: loopback{}, Captions: &Captions{Languages: languages, Client: client}},
		recovery.Attempt{
			Program:   media.Program{Inputs: []media.Input{{ID: media.PrimaryInputID, URL: &url.URL{Scheme: "http", Host: "origin.test", Path: "/v.m3u8"}}}},
			Rendition: source.Rendition{Captions: published},
		})
	t.Cleanup(func() { _ = s.releases.release() })
	return s
}

func TestTheSourcesCaptionIsServedToADeviceThatShowsThem(t *testing.T) {
	s := captionedAttempt(t, media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Captions: []media.CaptionFormat{media.CaptionSRT}}}}, []string{"en"})
	t.Cleanup(func() { _ = s.releases.release() })
	s.caption = s.fetchCaption()
	shown := s.shownCaptions()
	if len(shown) != 1 || shown[0].Format != media.CaptionSRT || !shown[0].Default {
		t.Fatalf("shown = %+v, want one SubRip caption", shown)
	}
	resp, err := http.Get(shown[0].URL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "00:00:01,000 --> 00:00:02,000\nhello") {
		t.Errorf("served %q", body)
	}
}

func TestNoCaptionIsShownWhenTheDeviceTakesNoneOrTheLanguageIsAbsent(t *testing.T) {
	for name, s := range map[string]*preparation{
		"a device with no caption format": captionedAttempt(t, media.Capabilities{}, []string{"en"}),
		"a language the source lacks":     captionedAttempt(t, media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Captions: []media.CaptionFormat{media.CaptionVTT}}}}, []string{"fr"}),
	} {
		if s.caption = s.fetchCaption(); s.caption != nil {
			t.Errorf("%s: fetched %+v, want none", name, s.caption)
		}
		if shown := s.shownCaptions(); len(shown) != 0 {
			t.Errorf("%s: shown = %+v, want none", name, shown)
		}
	}
}

func TestTheSourcesCaptionsReplaceTheTranscription(t *testing.T) {
	s := captionedAttempt(t, media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Captions: []media.CaptionFormat{media.CaptionVTT}}}}, []string{"en"})
	t.Cleanup(func() { _ = s.releases.release() })
	s.subtitles = func(context.Context) Transcript {
		t.Error("transcription started beside the source's own captions")
		return nil
	}
	s.caption = s.fetchCaption()
	if burn := s.transcription(t.Context(), facts{}, media.Program{}); burn != nil {
		t.Errorf("burn = %v, want none", burn)
	}
}

func TestBrokenPreferredCaptionTriesTheNextLanguage(t *testing.T) {
	s := captionedAttempt(t, media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Captions: []media.CaptionFormat{media.CaptionVTT}}}}, []string{"en", "fr"})
	s.attempt.Rendition.Captions = append(s.attempt.Rendition.Captions, source.Caption{
		URL: &url.URL{Scheme: "http", Host: "origin.test", Path: "/subs/fr.vtt"}, Language: "fr",
	})
	s.Captions.Client = textPages{
		"/subs/en.vtt": "broken captions",
		"/subs/fr.vtt": "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nbonjour\n",
	}
	t.Cleanup(func() { _ = s.releases.release() })
	s.caption = s.fetchCaption()
	shown := s.shownCaptions()
	if len(shown) != 1 || shown[0].Language != "fr" {
		t.Fatalf("shown = %+v, want the working French track", shown)
	}
}

func TestUnusableSourceCaptionsSelectTheTranscriptionPipeline(t *testing.T) {
	s := captionedAttempt(t, chromecastLike(media.MP4), []string{"en"})
	s.attempt.Program = programFromStream(t, handoffStream())
	s.subtitles = func(context.Context) Transcript { return nil }
	if got := s.compose(); got != compose.ReadOnce {
		t.Fatalf("missing source captions selected %s", got)
	}
	s.capabilities.Formats[0].Captions = []media.CaptionFormat{media.CaptionVTT}
	s.caption = &fetchedCaption{language: "en", format: media.CaptionVTT}
	if got := s.compose(); got != compose.Handoff {
		t.Fatalf("usable source captions selected %s", got)
	}
}

func TestRequestedSubtitlesWithoutPublishedTracksNeedPCM(t *testing.T) {
	s := captionedAttempt(t, chromecastLike(media.MP4), []string{"en"})
	s.attempt.Program = programFromStream(t, handoffStream())
	s.attempt.Rendition.Captions = nil
	s.subtitles = func(context.Context) Transcript { return nil }
	if got := s.compose(); got != compose.ReadOnce {
		t.Fatalf("requested transcription selected %s without PCM", got)
	}
	// The same source can be handed off when subtitles were disabled.
	s.Captions = nil
	s.subtitles = nil
	if got := s.compose(); got != compose.Handoff {
		t.Fatalf("disabled subtitles selected %s", got)
	}
}

func TestSourceSidecarPinsCompatiblePlaybackEnvelopes(t *testing.T) {
	caps := chromecastLike(media.MP4)
	// The direct source uses MP4, but only the HLS envelope accepts WebVTT.
	caps.Formats[0].Captions = nil
	hls := caps.Formats[0]
	hls.Transport = media.TransportHLS
	hls.Captions = []media.CaptionFormat{media.CaptionVTT}
	caps.Formats = append(caps.Formats, hls)
	s := captionedAttempt(t, caps, []string{"en"})
	s.attempt.Program = programFromStream(t, handoffStream())
	t.Cleanup(func() { _ = s.releases.release() })
	if got := s.compose(); got != compose.Handoff {
		t.Fatalf("uncaptioned control = %s, want handoff", got)
	}
	if s.caption = s.fetchCaption(); s.caption == nil {
		t.Fatal("source caption not selected")
	}
	selected := s.playbackCapabilities()
	if len(selected.Formats) != 1 || selected.Formats[0].Transport != media.TransportHLS {
		t.Fatalf("caption support crossed envelopes: %+v", selected.Formats)
	}
	if got := s.compose(); got == compose.Handoff {
		t.Fatal("MP4 handed off with captions advertised only for HLS")
	}
	if len(s.capabilities.Formats) != 2 {
		t.Fatal("selection mutated receiver capability snapshot")
	}
}
