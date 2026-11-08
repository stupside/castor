package hls

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
)

func TestFiniteMasterReadsHEVCAndSurroundFromTheProducedInitialization(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	encoders, err := exec.CommandContext(t.Context(), binary, "-encoders").CombinedOutput()
	if err != nil || !strings.Contains(string(encoders), "libx265") {
		t.Skip("HEVC fixture encoder unavailable")
	}
	dir := t.TempDir()
	plan := hlsPlan{runtime: 4 * time.Second}
	args := []string{"-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=24", "-f", "lavfi", "-i", "anullsrc=channel_layout=5.1:sample_rate=48000", "-t", "4", "-c:v", "libx265", "-x265-params", "pools=1:frame-threads=1:log-level=error", "-pix_fmt", "yuv420p10le", "-tag:v", "hvc1", "-c:a", "ac3", "-strict", "-2", "-f", "hls"}
	args = append(args, plan.Format().Tuning.Args...)
	args = append(args, "stream.m3u8")
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("HEVC fixture: %v: %s", err, out)
	}
	// Reproduce the muxer's missing CODECS attribute, even on versions which now write it.
	if err := os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nstream.m3u8\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := plan.Prepare(output.Opening{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Output.Close()
	server := httptest.NewServer(prepared.Output.Handler())
	defer server.Close()
	r, err := server.Client().Get(server.URL + "/" + prepared.Output.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(body), "hvc1.") || !strings.Contains(string(body), "ac-3") || strings.Contains(string(body), "wvtt") {
		t.Fatalf("actual native master: %d, %v, %s", r.StatusCode, err, body)
	}
}

func TestEventMasterDeclaresOriginalHEVCAndSurroundWithoutInventedSubtitles(t *testing.T) {
	for _, attributes := range []string{"BANDWIDTH=1000000", `BANDWIDTH=1000000,CODECS="avc1.42e01e,mp4a.40.2"`} {
		body := "#EXTM3U\n#EXT-X-STREAM-INF:" + attributes + "\nstream.m3u8\n"
		got := eventMaster(body, "hvc1.2.4.L150.B0,ac-3")
		if !strings.Contains(got, `CODECS="hvc1.2.4.L150.B0,ac-3"`) || strings.Contains(got, "wvtt") || strings.Contains(got, "avc1") {
			t.Fatalf("master=%s", got)
		}
	}
}

func TestFiniteEventDoesNotFinishWhileTheReceiverIsPaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		s := &Segments{runtime: time.Hour, o: output.Opening{IdleGrace: time.Second}, lastRequest: time.Now()}
		produced := make(chan struct{})
		close(produced)
		done := make(chan error, 1)
		go func() { done <- s.Wait(ctx, produced) }()
		synctest.Sleep(10 * time.Minute)
		select {
		case err := <-done:
			t.Fatalf("paused finite playback was ended by HTTP inactivity: %v", err)
		default:
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	})
}

func TestFiniteEventStartsAtZeroAndKeepsEarlierFragmentsAfterGrowth(t *testing.T) {
	dir := t.TempDir()
	plan := hlsPlan{runtime: time.Hour}
	prepared, err := plan.Prepare(output.Opening{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Output.Close()
	server := httptest.NewServer(prepared.Output.Handler())
	defer server.Close()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	get := func(name string) string {
		t.Helper()
		r, err := server.Client().Get(server.URL + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, err := io.ReadAll(r.Body)
		if err != nil || r.StatusCode != 200 || r.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("response: %d, %v", r.StatusCode, err)
		}
		return string(b)
	}
	first := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4,\nseg_00000.m4s\n"
	write("stream.m3u8", first)
	write("seg_00000.m4s", "original first fragment")
	if got := get("stream.m3u8"); !strings.HasPrefix(got, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n#EXT-X-PLAYLIST-TYPE:EVENT\n") || strings.Contains(got, "#EXT-X-ENDLIST") {
		t.Fatalf("initial playlist=%s", got)
	}
	write("stream.m3u8", first+"#EXTINF:4,\nseg_00001.m4s\n")
	if got := get("stream.m3u8"); !strings.Contains(got, "seg_00000.m4s") || !strings.Contains(got, "seg_00001.m4s") {
		t.Fatalf("growing playlist lost history: %s", got)
	}
	if got := get("seg_00000.m4s"); got != "original first fragment" {
		t.Fatalf("replayed bytes=%s", got)
	}
	write("stream.m3u8", first+"#EXT-X-ENDLIST\n")
	if got := get("stream.m3u8"); !strings.Contains(got, "#EXT-X-ENDLIST") {
		t.Fatalf("completed playlist=%s", got)
	}
}

func TestAnEventWithoutAVersionStartsRightAfterItsHeader(t *testing.T) {
	got := startAtBeginning("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n")
	if want := "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n#EXT-X-PLAYLIST-TYPE:EVENT\n"; got != want {
		t.Errorf("playlist = %q, want %q", got, want)
	}
}
