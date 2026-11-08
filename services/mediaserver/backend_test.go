package mediaserver

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestSourceCaptionSelectionDoesNotEnableTranscription(t *testing.T) {
	selection := &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Preferences{Preferences: &castorv1.SubtitlePreferences{Languages: []string{"en"}}}}
	cfg := defaults()
	if got := transcription(cfg.Servers.Media.Whisper, selection); got != nil {
		t.Fatal("selecting source captions started transcription")
	}
	selection.GetPreferences().TranscribeIfMissing = true
	if got := transcription(cfg.Servers.Media.Whisper, selection); got == nil {
		t.Fatal("explicit transcription did not enable the fallback")
	}
	disabled := &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Disabled{Disabled: &emptypb.Empty{}}}
	if got := transcription(cfg.Servers.Media.Whisper, disabled); got != nil {
		t.Fatal("disabled subtitles started transcription")
	}

	create := cfg.backend()
	for _, tc := range []struct {
		transcribe bool
		want       bool
	}{
		{false, false},
		{true, true},
	} {
		selected := &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Preferences{Preferences: &castorv1.SubtitlePreferences{Languages: []string{"en"}, TranscribeIfMissing: tc.transcribe}}}
		got := create(&mediav1.PlaybackSettings{Subtitles: selected}).(caster)
		if enabled := got.transcription != nil; enabled != tc.want {
			t.Errorf("transcribe=%v: fallback enabled=%v, want %v", tc.transcribe, enabled, tc.want)
		}
	}
}

func TestTranscriptionUsesThePreferredLanguageOfTheSelectedAudio(t *testing.T) {
	for _, tc := range []struct {
		preferred []string
		audio     string
		want      string
	}{
		{[]string{"fr", "en"}, "en-US", "en"},
		{[]string{"fr", "en"}, "fr-FR", "fr"},
		{[]string{"fr", "en"}, "de", ""},
		{[]string{"fr", "en"}, "", "fr"},
		{[]string{"fre", "eng"}, "fra", "fr"},
		{[]string{"ger"}, "", "de"},
	} {
		if got := transcriptLanguage(tc.preferred, tc.audio); got != tc.want {
			t.Errorf("preferred=%v audio=%q: got %q, want %q", tc.preferred, tc.audio, got, tc.want)
		}
	}
}

type originTransport func(*http.Request) (*http.Response, error)

func (f originTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBackendKeepsOriginCredentialsWithinEachCast(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = originTransport(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		if r.URL.Path == "/login" {
			h.Set("Set-Cookie", "session=cast-A; Path=/")
		}
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(r.Header.Get("Cookie"))), Request: r}, nil
	})
	cfg := defaults()
	create := cfg.backend()
	a := create(&mediav1.PlaybackSettings{}).(caster)
	b := create(&mediav1.PlaybackSettings{}).(caster)
	u, _ := url.Parse("https://origin.test/login")
	if _, _, _, err := a.client.Fetch(t.Context(), u, nil); err != nil {
		t.Fatal(err)
	}
	u.Path = "/media"
	body, _, _, err := b.client.Fetch(t.Context(), u, http.Header{"Cookie": {"session=cast-B"}})
	if err != nil || body != "session=cast-B" {
		t.Fatalf("cast B sent %q: %v", body, err)
	}
	body, _, _, err = a.client.Fetch(t.Context(), u, nil)
	if err != nil || body != "session=cast-A" {
		t.Fatalf("cast A lost its own session: %q: %v", body, err)
	}
}
