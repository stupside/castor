package hls

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage/diskbudget"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

func captionMP4(t *testing.T, duration string) []byte {
	t.Helper()
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25", "-t", duration, "-c:v", "libx264", "-g", "100", "-bf", "2", "-movflags", "delay_moov+default_base_moof", "-frag_duration", "4000000", "-min_frag_duration", "4000000", "-f", "mp4", "pipe:1")
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}
	return data
}

// captionWriter is a delivery whose writer cuts into a fresh directory, timed against cues.
func captionWriter(t *testing.T, cues *subtitle.Cues, videoOffset time.Duration) (*captionDelivery, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return &captionDelivery{root: root, playlist: "stream.m3u8", cues: cues, videoOffset: videoOffset, vtt: map[string]string{}}, dir
}

func TestCaptionPublisherBackpressuresAtUnsettledFragment(t *testing.T) {
	data := captionMP4(t, "12")
	cues := &subtitle.Cues{}
	c, dir := captionWriter(t, cues, 0)
	reader, writer := io.Pipe()
	defer writer.Close()
	written := make(chan error, 1)
	go func() { _, err := writer.Write(data); writer.Close(); written <- err }()
	published := make(chan error, 1)
	go func() { published <- c.write(t.Context(), reader) }()
	awaitCaptionFile(t, filepath.Join(dir, "fragment.tmp"))
	select {
	case err := <-written:
		t.Fatalf("producer drained beyond unsettled first fragment: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(dir, "stream.m3u8")); !os.IsNotExist(err) {
		t.Fatalf("unsettled playlist published: %v", err)
	}
	cues.Commit(nil, 4)
	awaitCaptionFile(t, filepath.Join(dir, "stream.m3u8"))
	playlist, err := os.ReadFile(filepath.Join(dir, "stream.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(playlist), "seg_00000.m4s") || strings.Contains(string(playlist), "seg_00001.m4s") {
		t.Fatalf("premature publication: %s", playlist)
	}
	select {
	case err := <-written:
		t.Fatalf("producer drained beyond unsettled second fragment: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cues.Close()
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	playlist, err = os.ReadFile(filepath.Join(dir, "stream.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(playlist), "#EXT-X-ENDLIST") || !strings.Contains(string(playlist), "seg_00002.m4s") {
		t.Fatalf("incomplete playlist: %s", playlist)
	}
}

func awaitCaptionFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("file not published: %s", path)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestCaptionPublisherBoundsRollingWindow(t *testing.T) {
	cues := &subtitle.Cues{}
	cues.Commit([]subtitle.Word{{Start: 1, End: 2, Text: "Before the window."}}, 44)
	cues.Close()
	c, dir := captionWriter(t, cues, 0)
	if err := c.write(t.Context(), bytes.NewReader(captionMP4(t, "44"))); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "seg_*.m4s"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 11 {
		t.Fatalf("retained %d fragments", len(files))
	}
	playlist, err := os.ReadFile(filepath.Join(dir, "stream.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(playlist), "#EXT-X-MEDIA-SEQUENCE:3") {
		t.Fatalf("window did not roll: %s", playlist)
	}
	if captions := captionRequest(t, c, captionPlaylist).Body.String(); !strings.Contains(captions, "#EXT-X-MEDIA-SEQUENCE:3") {
		t.Fatalf("captions did not roll with the video: %s", captions)
	}
	if kept := cues.Snapshot(0, 5).Cues; len(kept) != 0 {
		t.Errorf("cues before the window were kept: %v", kept)
	}
}

func TestCaptionPublisherReturnsWhenOwnerInterruptsRead(t *testing.T) {
	c, _ := captionWriter(t, &subtitle.Cues{}, 0)
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.write(ctx, reader) }()
	cancel()
	_ = reader.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled reader succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock pipe")
	}
}

func TestCaptionPublisherCancellationRemovesUnsettledFragment(t *testing.T) {
	c, dir := captionWriter(t, &subtitle.Cues{}, 0)
	ctx, cancel := context.WithCancel(t.Context())
	data := captionMP4(t, "8")
	done := make(chan error, 1)
	go func() { done <- c.write(ctx, bytes.NewReader(data)) }()
	awaitCaptionFile(t, filepath.Join(dir, "fragment.tmp"))
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited on transcript")
	}
	if _, err := os.Stat(filepath.Join(dir, "fragment.tmp")); !os.IsNotExist(err) {
		t.Fatalf("staged fragment retained: %v", err)
	}
}

func TestCaptionPublisherRejectsOversizedBoxBeforeReadingPayload(t *testing.T) {
	c, _ := captionWriter(t, &subtitle.Cues{}, 0)
	header := make([]byte, 8)
	binary.BigEndian.PutUint32(header, uint32(SegmentDirectoryLimit+1))
	copy(header[4:], "ftyp")
	err := c.write(t.Context(), bytes.NewReader(header))
	if !errors.Is(err, diskbudget.ErrLimit) {
		t.Fatalf("oversized input: %v", err)
	}
}

func TestCaptionPublisherNeverPublishesTruncatedFragment(t *testing.T) {
	data := captionMP4(t, "8")
	offset := 0
	for offset < len(data) {
		size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if string(data[offset+4:offset+8]) == "mdat" {
			data = data[:offset+size-1]
			break
		}
		offset += size
	}
	cues := &subtitle.Cues{}
	cues.Close()
	c, dir := captionWriter(t, cues, 0)
	if err := c.write(t.Context(), bytes.NewReader(data)); err == nil {
		t.Fatal("truncated media accepted")
	}
	for _, name := range []string{"stream.m3u8", "seg_00000.m4s", "fragment.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("partial output retained %s: %v", name, err)
		}
	}
}

func TestCaptionFragmentRetentionHonorsPlaylistLifetime(t *testing.T) {
	c, _ := captionWriter(t, &subtitle.Cues{}, 0)
	file, err := c.root.Create("old.m4s")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	c.vtt["caption-0.vtt"] = "WEBVTT\n"
	removed := time.Now()
	expiry := removed.Add(4*time.Second + 32*time.Second)
	fragments := []retiredCaptionFragment{{media: "old.m4s", captions: "caption-0.vtt", expires: expiry}}
	kept, err := c.expire(fragments, expiry.Add(-time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 {
		t.Fatal("fragment expired before last advertised playlist lifetime")
	}
	if _, err := c.root.Stat("old.m4s"); err != nil {
		t.Fatal(err)
	}
	if got := captionRequest(t, c, "caption-0.vtt").Code; got != http.StatusOK {
		t.Fatalf("captions of a fetchable fragment: %d", got)
	}
	kept, err = c.expire(kept, expiry)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Fatal("expired fragment retained")
	}
	if _, err := c.root.Stat("old.m4s"); !os.IsNotExist(err) {
		t.Fatalf("expired media still present: %v", err)
	}
	if _, kept := c.vtt["caption-0.vtt"]; kept {
		t.Fatal("captions retained after their video expired")
	}
}

func TestCaptionPublisherRefusesInventedVideoClock(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-hide_banner", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=8", "-c:a", "aac", "-movflags", "delay_moov+default_base_moof", "-frag_duration", "4000000", "-min_frag_duration", "4000000", "-f", "mp4", "pipe:1")
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	cues := &subtitle.Cues{}
	cues.Close()
	c, dir := captionWriter(t, cues, 6*time.Second)
	err = c.write(t.Context(), bytes.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "no video clock") {
		t.Fatalf("audio-only first fragment: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stream.m3u8")); !os.IsNotExist(err) {
		t.Fatalf("invented video clock published: %v", err)
	}
}
