package hls

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// captionSource is picture with B-frames beside sound, encoded into fragments as the caption encoder writes them; videoFilters retime the picture.
func captionSource(t *testing.T, videoFilters ...string) []byte {
	t.Helper()
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	args := []string{"-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25:duration=10", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=12", "-c:v", "libx264", "-g", "100", "-bf", "2", "-c:a", "aac"}
	args = append(args, videoFilters...)
	args = append(args, "-movflags", "delay_moov+default_base_moof", "-frag_duration", "4000000", "-min_frag_duration", "4000000", "-f", "mp4", "pipe:1")
	data, err := exec.CommandContext(t.Context(), binary, args...).Output()
	if err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}
	return data
}

func captionRequest(t *testing.T, c *captionDelivery, name string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	if !c.serve(recorder, httptest.NewRequest(http.MethodGet, "/"+name, nil)) {
		t.Fatal("request not routed")
	}
	return recorder
}

// awaitPublished waits until the writer has published a fragment.
func awaitPublished(t *testing.T, c *captionDelivery) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for captionRequest(t, c, "captions.m3u8").Code != http.StatusOK {
		select {
		case <-deadline:
			t.Fatal("no fragment published")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func readPlaylist(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "stream.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCaptionRenditionsWaitForCommittedCues(t *testing.T) {
	data := captionSource(t)
	cues := &subtitle.Cues{}
	c, dir := captionWriter(t, cues, 0)
	written := make(chan error, 1)
	go func() { written <- c.write(t.Context(), bytes.NewReader(data)) }()
	if got := captionRequest(t, c, "captions.m3u8").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("unsettled captions: %d", got)
	}
	cues.Commit([]subtitle.Word{{Start: 1, End: 2, Text: "Hello."}}, 4.2)
	awaitPublished(t, c)
	if video := readPlaylist(t, dir); !strings.Contains(video, "seg_00000.m4s") || strings.Contains(video, "seg_00001.m4s") || strings.Contains(video, "ENDLIST") {
		t.Fatalf("premature video publication: %s", video)
	}
	playlist := captionRequest(t, c, "captions.m3u8").Body.String()
	if !strings.Contains(playlist, "caption-0.vtt") || strings.Contains(playlist, "caption-1.vtt") {
		t.Fatalf("mismatched captions: %s", playlist)
	}
	first := captionRequest(t, c, "caption-0.vtt")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "Hello.") {
		t.Fatalf("caption response: %s", first.Body)
	}
	cues.Commit([]subtitle.Word{{Start: 4.5, End: 5.5, Text: "Next."}}, 12)
	cues.Close()
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if got := captionRequest(t, c, "caption-0.vtt").Body.String(); got != first.Body.String() {
		t.Fatal("published caption changed")
	}
	if got := readPlaylist(t, dir); !strings.Contains(got, "ENDLIST") || !strings.Contains(got, "seg_00002.m4s") {
		t.Fatalf("finished video: %s", got)
	}
	if got := captionRequest(t, c, "captions.m3u8").Body.String(); !strings.Contains(got, "ENDLIST") || !strings.Contains(got, "caption-2.vtt") {
		t.Fatalf("finished captions: %s", got)
	}
	master := captionRequest(t, c, "master.m3u8").Body.String()
	if !strings.Contains(master, "CODECS=\"avc1.") || !strings.Contains(master, ",mp4a.40.2,wvtt\"") || !strings.Contains(master, "TYPE=SUBTITLES") || !strings.Contains(master, "SUBTITLES=\"captions\"") {
		t.Fatalf("missing rendition association: %s", master)
	}
}

func TestACueAcrossAFragmentBoundaryPlaysWithBoth(t *testing.T) {
	data := captionSource(t)
	cues := &subtitle.Cues{}
	cues.Commit([]subtitle.Word{{Start: 3.5, End: 4.5, Text: "Across."}}, 12)
	cues.Close()
	c, _ := captionWriter(t, cues, 0)
	if err := c.write(t.Context(), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"caption-0.vtt", "caption-1.vtt"} {
		if got := captionRequest(t, c, name).Body.String(); !strings.Contains(got, "Across.") {
			t.Fatalf("%s lacks the cue it overlaps: %s", name, got)
		}
	}
	if got := captionRequest(t, c, "caption-2.vtt").Body.String(); strings.Contains(got, "Across.") {
		t.Fatalf("caption-2.vtt carries a cue ended before it: %s", got)
	}
}

func TestCaptionVTTClockEscapingAndEmptySegments(t *testing.T) {
	text := captionVTT([]subtitle.Cue{{Start: 3661.125, End: 3662, Text: "<b> & -->"}}, 0)
	if !strings.Contains(text, "01:01:01.125 --> 01:01:02.000") || !strings.Contains(text, "&lt;b&gt; &amp; --&gt;") {
		t.Fatal(text)
	}
	if empty := captionVTT(nil, 0); empty != "WEBVTT\nX-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:0\n\n" {
		t.Fatal(empty)
	}
}

func TestCaptionDeliveryRoutesThroughSegments(t *testing.T) {
	data := captionSource(t)
	o := testOpening{Publisher: newLAN(t),
		Format: format()}
	store, err := openSegmentStore(t.TempDir(), o.Format.Tuning.Output)
	if err != nil {
		t.Fatal(err)
	}
	cues := &subtitle.Cues{}
	store.captions = &captionDelivery{root: store.root, playlist: o.Format.Tuning.Output, cues: cues, vtt: map[string]string{}}
	srv, err := publishSegmentsFixture(t.Context(), o, store, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	if srv.URL().Path != "/master.m3u8" {
		t.Fatalf("URL = %s", srv.URL())
	}
	if got, _ := srv.Artifact().Landed(); got != 0 {
		t.Fatalf("unsettled artifact ready: %d", got)
	}
	cues.Commit(nil, 12)
	cues.Close()
	if err := store.captions.write(t.Context(), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if got, _ := srv.Artifact().Landed(); got == 0 {
		t.Fatal("settled artifact not ready")
	}
	for _, name := range []string{"/master.m3u8", "/stream.m3u8", "/captions.m3u8", "/caption-0.vtt"} {
		response, err := request(t.Context(), srv, http.MethodGet, name)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", name, response.StatusCode)
		}
	}
	if srv.Served() != 0 {
		t.Fatal("subtitle traffic counted as video delivery")
	}
}

func TestCaptionMediaClockIncludesSampleDurations(t *testing.T) {
	cues := &subtitle.Cues{}
	cues.Close()
	c, _ := captionWriter(t, cues, 0)
	if err := c.write(t.Context(), bytes.NewReader(captionSource(t))); err != nil {
		t.Fatal(err)
	}
	first, err := captionMedia(c.root, initSection, "seg_00000.m4s")
	if err != nil {
		t.Fatal(err)
	}
	next, err := captionMedia(c.root, initSection, "seg_00001.m4s")
	if err != nil {
		t.Fatal(err)
	}
	// The B-frames' composition offset is undone by the edit list: a clock that skipped either would start off zero.
	if first.VideoStart == nil || next.VideoStart == nil || *first.VideoStart != 0 || first.Start > *first.VideoStart || first.End < 4 || *next.VideoStart != 4 || next.End < 8 {
		t.Fatalf("media clocks: %+v, %+v", first, next)
	}
}

func TestCaptionTimelinePreservesAudioBeforeVideo(t *testing.T) {
	data := captionSource(t, "-vf", "setpts=PTS+2/TB", "-fps_mode", "passthrough")
	cues := &subtitle.Cues{}
	cues.Commit([]subtitle.Word{{Start: 0.3, End: 1.6, Text: "Before the picture."}, {Start: 2.5, End: 3.5, Text: "On the original clock."}}, 12)
	cues.Close()
	c, _ := captionWriter(t, cues, 2*time.Second)
	if err := c.write(t.Context(), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	text := captionRequest(t, c, "caption-0.vtt").Body.String()
	if !strings.Contains(text, "00:00:00.450 --> 00:00:01.400") || !strings.Contains(text, "Before the picture.") || !strings.Contains(text, "00:00:02.650 --> 00:00:03.300") {
		t.Fatalf("cue lost source clock: %s", text)
	}
	var ticks int64
	if _, err := fmt.Sscanf(text, "WEBVTT\nX-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:%d", &ticks); err != nil {
		t.Fatal(err)
	}
	if ticks > 1<<32 {
		ticks -= 1 << 33
	}
	// A two-second source video delay must not become a two-second subtitle shift.
	if origin := float64(ticks) / 90000; origin > 0.2 || origin < -0.2 {
		t.Fatalf("video delay leaked into mapping: %fs", origin)
	}
}
