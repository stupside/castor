package execute

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// ladderSeconds is longer than the burst a read gets ahead of a device by, so it is not complete when a cast moves.
const ladderSeconds = 100

// ladderOrigin serves a program as two HLS rungs whose keyframes fall together: a heavy 720p one and a light 360p one.
type ladderOrigin struct {
	server   *httptest.Server
	hi, lo   source.Rendition
	programs map[string]media.Program
}

func serveLadder(t *testing.T, ffmpegPath string) *ladderOrigin {
	t.Helper()
	dir := t.TempDir()
	for _, rung := range []struct {
		name, size, rate string
	}{{"hi", "1280x720", "2400k"}, {"lo", "640x360", "600k"}} {
		if err := os.Mkdir(filepath.Join(dir, rung.name), 0o700); err != nil {
			t.Fatal(err)
		}
		args := []string{
			"-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=size=" + rung.size + ":rate=25,format=yuv420p",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", strconv.Itoa(ladderSeconds), "-c:v", "libx264", "-preset", "veryfast", "-profile:v", "main", "-b:v", rung.rate, "-maxrate", rung.rate, "-bufsize", rung.rate,
			"-g", "50", "-keyint_min", "50", "-sc_threshold", "0",
			"-c:a", "aac", "-ac", "2", "-b:a", "128k",
			"-f", "hls", "-hls_time", "4", "-hls_playlist_type", "vod", "-hls_segment_type", "mpegts",
			"-hls_segment_filename", filepath.Join(dir, rung.name, "seg%d.ts"), filepath.Join(dir, rung.name, "index.m3u8"),
		}
		if out, err := exec.CommandContext(t.Context(), ffmpegPath, args...).CombinedOutput(); err != nil {
			t.Fatalf("generating the %s rung: %v\n%s", rung.name, err, out)
		}
	}
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(server.Close)
	l := &ladderOrigin{server: server, programs: map[string]media.Program{}}
	rung := func(name string, bitrate media.Bitrate, height int) source.Rendition {
		u, _ := url.Parse(server.URL + "/" + name + "/index.m3u8")
		r := source.Rendition{URL: u, Bitrate: bitrate, Height: height}
		l.programs[u.String()] = programFromStream(t, &source.Stream{URL: u, ContentType: media.HLS})
		return r
	}
	l.hi, l.lo = rung("hi", 2_600_000, 720), rung("lo", 800_000, 360)
	return l
}

// plan reads every input of a rung with a burst of a minute, then at playback pace.
func ladderPlan(program media.Program) fetch.Plan {
	return uniformFetchPlan(program, fetch.Policy{Deadline: testReadDeadline, SegmentRetries: 3, Pace: fetch.Pace{Realtime: 1, Burst: time.Minute, Catchup: 2}})
}

func (l *ladderOrigin) Resolve(_ context.Context, _ *source.Stream, chosen source.Rendition, _ media.Bitrate) (source.Resolution, error) {
	program, ok := l.programs[chosen.URL.String()]
	if !ok {
		return source.Resolution{}, fmt.Errorf("no such rung: %s", chosen.URL)
	}
	return source.Resolution{Program: program.Clone(), Rendition: chosen}, nil
}

// frames is the width of each picture of a stream by second since its first, and the biggest jump its timestamps make.
func frames(t *testing.T, ffprobePath, file string) (widths map[int]map[int]int, jump float64) {
	t.Helper()
	out, err := exec.Command(ffprobePath, "-v", "error", "-select_streams", "v", "-show_entries", "frame=pts_time,width", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	reader := csv.NewReader(strings.NewReader(string(out)))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	widths = map[int]map[int]int{}
	last, origin := -1.0, -1.0
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		width, _ := strconv.Atoi(row[0])
		pts, _ := strconv.ParseFloat(row[1], 64)
		if width == 0 {
			width, _ = strconv.Atoi(row[1])
			pts, _ = strconv.ParseFloat(row[0], 64)
		}
		if origin < 0 {
			origin = pts
		}
		second := int(pts - origin)
		if widths[second] == nil {
			widths[second] = map[int]int{}
		}
		widths[second][width]++
		if last >= 0 && pts-last > jump {
			jump = pts - last
		}
		last = pts
	}
	return widths, jump
}

// moved runs a cast of the ladder's heavy rung on caps, switches it to ceiling once its device has taken head bytes, and returns it with after bytes more.
func moved(t *testing.T, caps media.Capabilities, head, after int, ceiling media.Bitrate) (served []byte, ladder *ladderOrigin, p presentation) {
	t.Helper()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	ladder = serveLadder(t, ffmpegPath)
	c := realCast(&fakeDevice{caps: caps}, ffmpegPath, ffprobePath)
	c.Resolver = ladder
	program := ladder.programs[ladder.hi.URL.String()]
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	t.Cleanup(cancel)
	s := testPreparation(ctx, c, recovery.Attempt{
		Try: 1, Program: program, Fetch: ladderPlan(program),
		Stream: &source.Stream{URL: ladder.hi.URL, ContentType: media.HLS}, Origin: source.Origin{Renditions: []source.Rendition{ladder.hi, ladder.lo}}, Rendition: ladder.hi,
	})
	t.Cleanup(func() { _ = s.releases.release() })
	var err error
	if p, err = s.preparePresentation(); err != nil {
		t.Fatalf("preparing: %v", err)
	}
	if p.delivery == nil || p.delivery.rate == nil {
		t.Fatalf("the delivery cannot change: %+v", p.delivery)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.target.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	first := make([]byte, head)
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatalf("reading the head of the stream: %v", err)
	}
	from, err := p.delivery.rate.Switch(ctx, ceiling)
	if err != nil {
		t.Fatalf("switching to %s: %v", ceiling, err)
	}
	// Some seconds of what follows the cut; the rest of the program is no part of what is checked.
	rest := make([]byte, max(int(from), head)+after-head)
	n, err := io.ReadFull(resp.Body, rest)
	if err != nil {
		t.Fatalf("reading what follows the switch: %v (%d bytes)", err, n)
	}
	served = append(first, rest[:n]...)
	return served[:len(served)/packet*packet], ladder, p
}

// packet is the size of a transport stream packet.
const packet = 188

func checkMovedStream(t *testing.T, served []byte, wantLate int) {
	t.Helper()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	file := filepath.Join(t.TempDir(), "served.ts")
	if err := os.WriteFile(file, served, 0o600); err != nil {
		t.Fatal(err)
	}
	widths, jump := frames(t, ffprobePath, file)
	if jump > 0.5 {
		t.Errorf("the timestamps jump %.2fs somewhere: the cut did not join one timeline", jump)
	}
	last := len(widths) - 1
	if early := widths[1]; early[1280] == 0 {
		t.Errorf("the pictures at the start are %v, want the heavy rung's 1280", early)
	}
	if late := widths[last-1]; late[wantLate] == 0 || len(late) != 1 {
		t.Errorf("the pictures near the end are %v, want only %d wide", late, wantLate)
	}
	if last < 20 {
		t.Errorf("the stream holds %d seconds of pictures, want some after the cut", last)
	}
	// What was served is cut short, so the last seconds are not asked to decode.
	out, err := exec.Command(ffmpegPath, "-v", "error", "-i", file, "-t", strconv.Itoa(last-3), "-f", "null", "-").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Errorf("decoding what was served: %v\n%s", err, out)
	}
}

// A buffer served as it is carries on from the lighter rung of the source when its device cannot take the heavy one, with no encode at all.
func TestAStreamServedAsItIsMovesToALighterRungOfItsSource(t *testing.T) {
	served, _, p := moved(t, dlnaLike(), 400<<10, 3<<20, 1_200_000)
	checkMovedStream(t, served, 640)
	if p.reached.reader.tail() == p.reached.reader {
		t.Error("the read was not carried on by the lighter rung")
	}
}

// The same, from a read well into the program, which the lighter rung is started a little before.
func TestAStreamFarIntoTheProgramMovesToALighterRungToo(t *testing.T) {
	served, _, _ := moved(t, dlnaLike(), 9<<20, 3<<20, 1_200_000)
	checkMovedStream(t, served, 640)
}

// A delivery made by encoders moves to the lighter rung too, and its sound is made as it was.
func TestADeliveryFromAnEncoderMovesToALighterRungOfItsSource(t *testing.T) {
	served, _, _ := moved(t, ac3Only(), 400<<10, 9<<20, 1_200_000)
	checkMovedStream(t, served, 640)
}

// The cast that read the lighter rung is asked for more once its device can take it, and goes back to the heavy one.
func TestACastOnALighterRungGoesBackToTheHeavyOne(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	ladder := serveLadder(t, ffmpegPath)
	c := realCast(&fakeDevice{caps: dlnaLike()}, ffmpegPath, ffprobePath)
	c.Resolver = ladder
	program := ladder.programs[ladder.hi.URL.String()]
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	s := testPreparation(ctx, c, recovery.Attempt{
		Try: 1, Program: program, Fetch: ladderPlan(program),
		Stream: &source.Stream{URL: ladder.hi.URL, ContentType: media.HLS}, Origin: source.Origin{Renditions: []source.Rendition{ladder.hi, ladder.lo}}, Rendition: ladder.hi,
	})
	t.Cleanup(func() { _ = s.releases.release() })
	p, err := s.preparePresentation()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.target.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var served []byte
	take := func(n int) {
		chunk := make([]byte, n)
		got, err := io.ReadFull(resp.Body, chunk)
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		served = append(served, chunk[:got]...)
	}
	take(600 << 10)
	if _, err := p.delivery.rate.Switch(ctx, 1_200_000); err != nil {
		t.Fatalf("switch to the lighter rung: %v", err)
	}
	// Time for the first cut to reach the device before the next.
	take(3 << 20)
	if _, err := p.delivery.rate.Switch(ctx, 0); err != nil {
		t.Fatalf("switch back to the heavy rung: %v", err)
	}
	take(9 << 20)
	served = served[:len(served)/packet*packet]
	file := filepath.Join(t.TempDir(), "served.ts")
	if err := os.WriteFile(file, served, 0o600); err != nil {
		t.Fatal(err)
	}
	widths, jump := frames(t, ffprobePath, file)
	if jump > 0.5 {
		t.Errorf("the timestamps jump %.2fs somewhere across two moves", jump)
	}
	last := len(widths) - 1
	if mid := widths[last/2]; mid[640] == 0 && mid[1280] == 0 {
		t.Errorf("the pictures in the middle are %v", mid)
	}
	if end := widths[last-1]; end[1280] == 0 || len(end) != 1 {
		t.Errorf("the pictures near the end are %v, want the heavy rung's again", end)
	}
	seen := map[int]bool{}
	for _, byWidth := range widths {
		for width := range byWidth {
			seen[width] = true
		}
	}
	if !seen[640] || !seen[1280] {
		t.Errorf("the pictures were of widths %v, want both rungs' to have been served", seen)
	}
	out, err := exec.Command(ffmpegPath, "-v", "error", "-i", file, "-t", strconv.Itoa(last-3), "-f", "null", "-").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Errorf("decoding what was served: %v\n%s", err, out)
	}
}
