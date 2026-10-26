package dash

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
)

// ffmpegPresentation is how ffmpeg's packager writes a ladder: one set per rung, sound apart.
const ffmpegPresentation = `<?xml version="1.0" encoding="utf-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT1H30M5.5S">
  <Period id="0" start="PT0.0S">
    <AdaptationSet id="0" contentType="video" maxWidth="1920" maxHeight="1080">
      <Representation id="0" mimeType="video/mp4" codecs="avc1.640028" bandwidth="6941000" width="1920" height="1080"/>
    </AdaptationSet>
    <AdaptationSet id="1" contentType="video" maxWidth="1280" maxHeight="720">
      <Representation id="1" mimeType="video/mp4" codecs="avc1.64001f" bandwidth="2400000" width="1280" height="720"/>
    </AdaptationSet>
    <AdaptationSet id="2" contentType="audio">
      <Representation id="2" mimeType="audio/mp4" codecs="mp4a.40.2" bandwidth="128000"/>
    </AdaptationSet>
  </Period>
</MPD>`

func resolveWith(t *testing.T, body string, chosen source.Rendition, ceiling media.HeightCap) source.Resolution {
	t.Helper()
	resolver := source.NewResolver(&sourcetest.Document{Body: body, Status: http.StatusOK}, ceiling, nil, source.Formats{Format{}})
	stream := &source.Stream{URL: sourcetest.URL(t, "https://origin.example/manifest.mpd"), ContentType: media.DASH}
	resolved, err := resolver.Resolve(t.Context(), stream, chosen, 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolved
}

func TestARungSettledEarlierIsReadAgain(t *testing.T) {
	resolved := resolveWith(t, ffmpegPresentation, source.Rendition{Representation: "1"}, 1080)
	if got := resolved.Rendition.Representation; got != "1" {
		t.Errorf("a recovery asking for representation 1 read %q", got)
	}
}

func TestARungWeighsTheSoundReadBesideIt(t *testing.T) {
	const body = `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT1H"><Period>
  <AdaptationSet contentType="video">
    <Representation id="1080" codecs="avc1.640028" bandwidth="6000000" height="1080"/>
    <Representation id="720" codecs="avc1.64001f" bandwidth="2400000" height="720"/>
    <Representation id="480" codecs="avc1.64001e" bandwidth="1200000" height="480"/>
    <Representation id="muxed" codecs="avc1.64001e,mp4a.40.2" bandwidth="1000000" height="360"/>
  </AdaptationSet>
  <AdaptationSet contentType="audio"><Representation id="sound" codecs="mp4a.40.2" bandwidth="128000"/></AdaptationSet>
</Period></MPD>`
	resolver := source.NewResolver(&sourcetest.Document{Body: body, Status: http.StatusOK}, 1080, nil, source.Formats{Format{}})
	stream := &source.Stream{URL: sourcetest.URL(t, "https://origin.example/manifest.mpd"), ContentType: media.DASH}
	// 2.4 Mbit/s of picture fits under 2.45, but not with the sound it is read with.
	resolved, err := resolver.Resolve(t.Context(), stream, source.Rendition{}, 2_450_000)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Rendition; got.Representation != "480" || got.Bitrate != 1_328_000 {
		t.Errorf("chosen %s at %d, want the 480 rung at its 1.2 Mbit/s and the sound's 128 kbit/s", got.Representation, got.Bitrate)
	}
	for _, r := range resolved.Origin.Renditions {
		if r.Representation == "muxed" && r.Bitrate != 1_000_000 {
			t.Errorf("a rung whose segments carry their sound weighs %d, want its own 1000000", r.Bitrate)
		}
	}
}

// laddered declares height on the set only for one rung, as packagers often do.
const laddered = `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT1H">
  <Period>
    <AdaptationSet contentType="video" maxHeight="2160">
      <Representation id="uhd" mimeType="video/mp4" codecs="hvc1.2.4.L150.90" bandwidth="17000000" width="3840"/>
    </AdaptationSet>
    <AdaptationSet contentType="video">
      <Representation id="hd" mimeType="video/mp4" codecs="avc1.640028" bandwidth="6941000" width="1920" height="1080"/>
      <Representation id="hd-hevc" mimeType="video/mp4" codecs="hvc1.1.6.L120.90" bandwidth="6941000" width="1920" height="1080"/>
    </AdaptationSet>
  </Period>
</MPD>`

func TestARungThatStatesNoHeightInheritsItsSetsAndCannotSlipPastTheCap(t *testing.T) {
	resolved := resolveWith(t, laddered, source.Rendition{}, 1080)
	if got := resolved.Rendition; got.Representation != "hd" {
		t.Errorf("chosen %+v, want the 1080 H.264 rung: the 2160 one inherits its set's height, and equal rungs prefer H.264", got)
	}
	got, measured := resolved.Program.Declaration()
	want := media.Declaration{VideoCodec: media.CodecH264, VideoProfile: "High", VideoHeight: 1080, VideoBitDepth: 8, VideoLevel: 40}
	if !measured || !reflect.DeepEqual(got, want) {
		t.Errorf("measurement = %+v, want the chosen representation's declaration %+v", got, want)
	}
}

func TestLivenessComesFromThePresentationsType(t *testing.T) {
	const dynamic = `<MPD type="dynamic" xmlns="urn:mpeg:dash:schema:mpd:2011"><Period>
  <AdaptationSet contentType="video"><Representation id="v" bandwidth="800000" width="640" height="360"/></AdaptationSet>
</Period></MPD>`
	for body, live := range map[string]bool{dynamic: true, ffmpegPresentation: false} {
		resolved := resolveWith(t, body, source.Rendition{}, 1080)
		if resolved.Origin.Live != live || sourcetest.PrimaryInput(t, resolved.Program).Fetching().Live != live {
			t.Errorf("live = %v, want %v", resolved.Origin.Live, live)
		}
	}
}

// stitched is a film cut around an ad Period that carries no sound of its own.
const stitched = `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static">
  <Period id="film" duration="PT10S">
    <AdaptationSet contentType="video"><Representation id="v" codecs="avc1.64001f" bandwidth="800000" height="360"/></AdaptationSet>
    <AdaptationSet contentType="audio" lang="en"><Role schemeIdUri="urn:mpeg:dash:role:2011" value="main"/>
      <Representation id="en" codecs="mp4a.40.2" bandwidth="96000"/></AdaptationSet>
    <AdaptationSet contentType="audio" lang="en"><Representation id="en-ac3" codecs="ac-3" bandwidth="384000"/></AdaptationSet>
  </Period>
  <Period id="ad" duration="PT2S">
    <AdaptationSet contentType="video"><Representation id="ad-v" codecs="avc1.64001f" bandwidth="2000000" height="720"/></AdaptationSet>
  </Period>
</MPD>`

func TestAStitchedPresentationIsSplicedAndPlaysToItsLongestInput(t *testing.T) {
	resolved := resolveWith(t, stitched, source.Rendition{}, 1080)
	if !resolved.Origin.Spliced || !sourcetest.PrimaryInput(t, resolved.Program).Fetch.Spliced {
		t.Error("a presentation cut into Periods is not marked spliced")
	}
	if audio := sourcetest.RequireInput(t, resolved.Program, media.AudioInputID); audio.Representation != "en" {
		t.Errorf("sound reads %q, want the main AAC track over a richer AC-3 one", audio.Representation)
	}
	if resolved.Program.EndPolicy != media.EndAtLongest {
		t.Errorf("end policy %s: the ad has no sound, so ending at the shortest input would cut the film", resolved.Program.EndPolicy)
	}
	if resolved.Origin.Duration.Seconds() != 12 {
		t.Errorf("duration %v, want the 12s both Periods add up to", resolved.Origin.Duration)
	}
}

func TestDASHAudioFollowsOrderedLanguagePreferences(t *testing.T) {
	const body = `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT1M"><Period>
  <AdaptationSet contentType="video"><Representation id="v" codecs="avc1.64001f" bandwidth="800000" height="360"/></AdaptationSet>
  <AdaptationSet contentType="audio" lang="en"><Role schemeIdUri="urn:mpeg:dash:role:2011" value="main"/>
    <Representation id="en" codecs="mp4a.40.2" bandwidth="128000"/></AdaptationSet>
  <AdaptationSet contentType="audio" lang="fr-FR">
    <Representation id="fr" codecs="mp4a.40.2" bandwidth="96000"/></AdaptationSet>
</Period></MPD>`
	for _, tc := range []struct {
		preferences []string
		wantTrack   string
		wantLang    string
	}{
		{[]string{"fr", "en"}, "fr", "fr-FR"},
		{[]string{"de", "en"}, "en", "en"},
		{[]string{"de"}, "en", "en"},
	} {
		resolver := source.NewResolver(&sourcetest.Document{Body: body, Status: http.StatusOK}, 1080, tc.preferences, source.Formats{Format{}})
		stream := &source.Stream{URL: sourcetest.URL(t, "https://origin.example/manifest.mpd"), ContentType: media.DASH}
		got, err := resolver.Resolve(t.Context(), stream, source.Rendition{}, 0)
		if err != nil {
			t.Fatal(err)
		}
		track := sourcetest.RequireInput(t, got.Program, media.AudioInputID)
		if track.Representation != tc.wantTrack || got.Rendition.AudioLanguage != tc.wantLang {
			t.Errorf("preferences %v selected %q (%q), want %q (%q)", tc.preferences, track.Representation, got.Rendition.AudioLanguage, tc.wantTrack, tc.wantLang)
		}
	}
}

// A presentation under DRM is refused at resolution, so recovery moves to the next link rather than failing a read.
func TestAPresentationUnderDRMIsRefused(t *testing.T) {
	const body = `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT1H">
  <Period><AdaptationSet contentType="video">
    <ContentProtection schemeIdUri="urn:mpeg:dash:mp4protection:2011" value="cenc"/>
    <Representation id="hd" mimeType="video/mp4" codecs="avc1.640028" bandwidth="6941000" height="1080"/>
  </AdaptationSet></Period></MPD>`
	resolver := source.NewResolver(&sourcetest.Document{Body: body, Status: http.StatusOK}, 1080, nil, source.Formats{Format{}})
	stream := &source.Stream{URL: sourcetest.URL(t, "https://origin.example/manifest.mpd"), ContentType: media.DASH}
	if _, err := resolver.Resolve(t.Context(), stream, source.Rendition{}, 0); err == nil || !strings.Contains(err.Error(), "cenc") {
		t.Errorf("Resolve = %v, want a refusal naming the protection", err)
	}
}

func TestThumbnailsAndTrickPlayNeverJoinTheLadder(t *testing.T) {
	const body = mpdOpen + `type="static" mediaPresentationDuration="PT1H"><Period>
  <AdaptationSet contentType="video"><Representation id="film" codecs="avc1.64001f" bandwidth="800000" height="360"/></AdaptationSet>
  <AdaptationSet contentType="image" mimeType="image/jpeg"><Representation id="tiles" bandwidth="1000" width="1600" height="900"/></AdaptationSet>
  <AdaptationSet contentType="video"><EssentialProperty schemeIdUri="http://dashif.org/guidelines/trickmode" value="1"/>
    <Representation id="trick" codecs="avc1.64001f" bandwidth="100000" height="720"/></AdaptationSet>
</Period></MPD>`
	resolved := resolveWith(t, body, source.Rendition{}, 1080)
	if len(resolved.Origin.Renditions) != 1 || resolved.Rendition.Representation != "film" {
		t.Errorf("ladder = %+v, want the film alone", resolved.Origin.Renditions)
	}
}

func TestALiveLadderIsTheOpenPeriods(t *testing.T) {
	const body = mpdOpen + `type="dynamic" availabilityStartTime="2026-01-01T00:00:00Z"><Period id="ad" start="PT0S" duration="PT600S">
  <AdaptationSet contentType="video"><Representation id="ad" codecs="avc1.64001f" bandwidth="4000000" height="1080"/></AdaptationSet></Period>
  <Period id="now" start="PT600S"><AdaptationSet contentType="video"><Representation id="now" codecs="avc1.64001f" bandwidth="1000000" height="720"/></AdaptationSet></Period></MPD>`
	if got := resolveWith(t, body, source.Rendition{}, 720).Rendition; got.Representation != "now" {
		t.Errorf("chosen %+v, want the running Period's 720p rung, not the finished ad's", got)
	}
}

func TestCalendarUnitsWrittenAsZerosStillReadAsADuration(t *testing.T) {
	runtime := func(stated string) time.Duration {
		body := mpdOpen + `type="static" mediaPresentationDuration="` + stated + `"><Period>
  <AdaptationSet contentType="video"><Representation id="v" codecs="avc1.64001f" bandwidth="800000" height="360"/></AdaptationSet></Period></MPD>`
		return resolveWith(t, body, source.Rendition{}, 1080).Origin.Duration
	}
	if got := runtime("P0Y0M0DT0H3M30S"); got != 210*time.Second {
		t.Errorf("P0Y0M0DT0H3M30S = %v, want 3m30s", got)
	}
	if got := runtime("P1M"); got != 0 {
		t.Errorf("P1M = %v, want nothing: a month has no fixed length", got)
	}
}
