package mpegts

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
)

// a second of pictures to a keyframe, as ffmpeg muxes them: what the index must read in a real stream.
func ffmpegStream(t *testing.T) []byte {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg to make a stream with")
	}
	file := filepath.Join(t.TempDir(), "in.ts")
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=25", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "6", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-f", "mpegts", file).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestACutInARealStreamStartsAReaderAtAKeyframeAtItsTime(t *testing.T) {
	data := ffmpegStream(t)
	x := splice.NewIndex(Framing{})
	for rest := data; len(rest) > 0; {
		n := min(len(rest), 4096)
		_, _ = x.Write(rest[:n])
		rest = rest[n:]
	}
	var cuts []splice.Cut
	for at := int64(0); ; {
		cut, ok := x.After(at)
		if !ok {
			break
		}
		cuts = append(cuts, cut)
		at = cut.Offset + 1
	}
	if len(cuts) < 5 {
		t.Fatalf("listed %d cuts in 6 seconds of one-second keyframes, want at least 5", len(cuts))
	}
	for i := 1; i < len(cuts); i++ {
		if gap := (cuts[i].PTS - cuts[i-1].PTS).Seconds(); math.Abs(gap-1) > 0.05 {
			t.Errorf("cuts %d and %d are %.2fs apart, want 1s", i-1, i, gap)
		}
	}

	// A reader given the tables and the bytes from a cut finds a picture it can decode at the cut's time.
	cut := cuts[3]
	tail := filepath.Join(t.TempDir(), "tail.ts")
	if err := os.WriteFile(tail, bytes.Join([][]byte{cut.Tables, data[cut.Offset:]}, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe to read the tail with")
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts_time,flags",
		"-read_intervals", "%+#1", "-of", "csv=p=0", tail).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "K") {
		t.Fatalf("the first packet at the cut is %q, want a keyframe", out)
	}
	if pts, _ := strconv.ParseFloat(fields[0], 64); math.Abs(pts-cut.PTS.Seconds()) > 0.001 {
		t.Errorf("the first picture at the cut is at %.3fs, the index said %s", pts, cut.PTS.Round(time.Millisecond))
	}
}
