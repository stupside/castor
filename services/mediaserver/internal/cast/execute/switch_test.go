package execute

import (
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// heavy serves a minute of constant-bitrate H.264/AAC, a keyframe every second, which a lighter cast must weigh well under.
func serveHeavy(t *testing.T, ffmpegPath string) fixtureOrigin {
	t.Helper()
	path := generateFixture(t, ffmpegPath, "heavy.mp4", 60, []string{
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-profile:v", "baseline",
		"-b:v", "2500k", "-minrate", "2500k", "-maxrate", "2500k", "-bufsize", "1000k", "-x264-params", "nal-hrd=cbr:force-cfr=1",
		"-g", "15", "-keyint_min", "15", "-sc_threshold", "0",
		"-c:a", "aac", "-ac", "2", "-shortest", "-movflags", "+faststart",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, path) }))
	t.Cleanup(server.Close)
	return fixtureOrigin{server: server, path: "/heavy.mp4", contentType: media.MP4}
}

// ac3Only is a device that takes MPEG-TS with sound only as AC-3, so the cast re-encodes the sound and an encoder is always at work.
func ac3Only() media.Capabilities {
	return media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAC3, MaxChannels: 2}}}}
}

// bitrates is the video bytes a second of a transport stream carries, by second of presentation time, and the biggest jump its timestamps make.
func bitrates(t *testing.T, ffprobePath, file string) (perSecond map[int]float64, jump float64) {
	t.Helper()
	out, err := exec.Command(ffprobePath, "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts_time,size", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	perSecond = map[int]float64{}
	last := -1.0
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		pts, _ := strconv.ParseFloat(row[0], 64)
		size, _ := strconv.ParseFloat(row[1], 64)
		perSecond[int(pts)] += size * 8
		if last >= 0 && pts-last > jump {
			jump = pts - last
		}
		last = pts
	}
	return perSecond, jump
}

// switched runs a cast on a device that reads slowly at first, switches the cast to ceiling a little into it, and returns what the device was served.
func switched(t *testing.T, caps media.Capabilities, ceiling media.Bitrate) (served []byte, from int64) {
	t.Helper()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveHeavy(t, ffmpegPath)
	dev := &fakeDevice{caps: caps}
	c := realCast(dev, ffmpegPath, ffprobePath)
	program := programFromStream(t, origin.stream())

	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	s := testPreparation(ctx, c, recovery.Attempt{Try: 1, Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})
	t.Cleanup(func() { _ = s.releases.release() })
	p, err := s.preparePresentation()
	if err != nil {
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
	defer resp.Body.Close()
	// A device that has taken the first of the stream, as it would be some seconds in.
	head := make([]byte, 400<<10)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		t.Fatalf("reading the head of the stream: %v", err)
	}
	if from, err = p.delivery.rate.Switch(ctx, ceiling); err != nil {
		t.Fatalf("switching to %s: %v", ceiling, err)
	}
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the rest of the stream: %v", err)
	}
	return append(head, rest...), from
}

func checkSwitchedStream(t *testing.T, served []byte, ceiling media.Bitrate) {
	t.Helper()
	_, ffprobePath := requireFFmpegTools(t)
	file := filepath.Join(t.TempDir(), "served.ts")
	if err := os.WriteFile(file, served, 0o600); err != nil {
		t.Fatal(err)
	}
	perSecond, jump := bitrates(t, ffprobePath, file)
	if jump > 0.5 {
		t.Errorf("the timestamps jump %.2fs somewhere: the cut did not join one timeline", jump)
	}
	var early, late float64
	var earlyN, lateN int
	for sec, bits := range perSecond {
		switch {
		case sec <= 3:
			early, earlyN = early+bits, earlyN+1
		case sec >= 40 && sec <= 55:
			late, lateN = late+bits, lateN+1
		}
	}
	if earlyN == 0 || lateN == 0 {
		t.Fatalf("seconds of video by time: %v", perSecond)
	}
	early, late = early/float64(earlyN), late/float64(lateN)
	if !(late < early*0.7) {
		t.Errorf("the video weighed %.0f bit/s at the start and %.0f at the end, want the end well under the start after a switch to %s", early, late, ceiling)
	}
	if late > float64(ceiling) {
		t.Errorf("the video weighed %.0f bit/s at the end, over the %s asked", late, ceiling)
	}
	// Whatever is served decodes from end to end.
	out, err := exec.Command(strings.TrimSuffix(ffprobePath, "ffprobe")+"ffmpeg", "-v", "error", "-i", file, "-f", "null", "-").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Errorf("decoding what was served: %v\n%s", err, out)
	}
	if last := lastSecond(perSecond); last < 58 {
		t.Errorf("the stream ends at %ds, want the whole minute", last)
	}
}

func lastSecond(perSecond map[int]float64) int {
	last := 0
	for sec := range perSecond {
		last = max(last, sec)
	}
	return last
}

// A buffer served as it is carries on from an encoder at the ceiling after the cut, and the device sees one stream.
func TestAStreamServedAsItIsCarriesOnAtALighterRate(t *testing.T) {
	served, from := switched(t, dlnaLike(), 1_200_000)
	if from <= 0 {
		t.Errorf("the switch took effect after byte %d, want a cut in the read's buffer", from)
	}
	checkSwitchedStream(t, served, 1_200_000)
}

// A delivery with an encoder at work is cut to a new one at the ceiling.
func TestADeliveryFromAnEncoderIsCutToALighterOne(t *testing.T) {
	served, from := switched(t, ac3Only(), 1_200_000)
	if from != 0 {
		t.Errorf("the switch of a delivery made by encoders took effect after byte %d, want the chain to carry the bytes", from)
	}
	checkSwitchedStream(t, served, 1_200_000)
}

// prepared is a cast of the heavy program on caps, ready and not yet fetched by its device.
func prepared(t *testing.T, caps media.Capabilities) (presentation, context.Context) {
	t.Helper()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveHeavy(t, ffmpegPath)
	c := realCast(&fakeDevice{caps: caps}, ffmpegPath, ffprobePath)
	program := programFromStream(t, origin.stream())
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	t.Cleanup(cancel)
	s := testPreparation(ctx, c, recovery.Attempt{Try: 1, Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})
	t.Cleanup(func() { _ = s.releases.release() })
	p, err := s.preparePresentation()
	if err != nil {
		t.Fatalf("preparing: %v", err)
	}
	return p, ctx
}

// A delivery made by encoders runs only a lead ahead of a device that has not asked for more, however much the read holds.
func TestADeliveryRunsNoFurtherAheadOfItsDeviceThanTheLead(t *testing.T) {
	p, _ := prepared(t, ac3Only())
	time.Sleep(1500 * time.Millisecond)
	made := p.delivery.output.Progress()
	spooled := p.reached.reader.spool.Size()
	if made.Bytes <= 0 || made.Bytes > spooled/2 {
		t.Errorf("the delivery made %d bytes of the %d the read holds, want only a lead of them", made.Bytes, spooled)
	}
	if !p.delivery.rate.Withheld() {
		t.Error("a delivery ahead of a device that asks for nothing is not holding itself back")
	}
}

// A switch asked for where the stream has no keyframe left to cut at is refused, and the stream is served on as it was.
func TestASwitchPastTheLastKeyframeIsRefusedAndTheStreamIsServedAsItWas(t *testing.T) {
	p, ctx := prepared(t, dlnaLike())
	// The read is whole; a device that has been written all of it has no cut ahead of it.
	<-p.reached.reader.Done()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.target.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.delivery.rate.Switch(ctx, 1_200_000); err == nil {
		t.Error("a switch past the last keyframe was accepted")
	}
	spooled, _ := os.ReadFile(p.reached.reader.spool.Path())
	if string(body) != string(spooled) {
		t.Errorf("the stream served %d bytes, want the read's %d as it was", len(body), len(spooled))
	}
}

// Lighter and then heavier again is one timeline.
func TestACastLightenedThenLiftedIsOneStream(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveHeavy(t, ffmpegPath)
	c := realCast(&fakeDevice{caps: dlnaLike()}, ffmpegPath, ffprobePath)
	program := programFromStream(t, origin.stream())
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	s := testPreparation(ctx, c, recovery.Attempt{Try: 1, Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})
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
	chunk := make([]byte, 300<<10)
	for step, ceiling := range []media.Bitrate{1_200_000, 0} {
		n, err := io.ReadFull(resp.Body, chunk)
		if err != nil {
			t.Fatalf("reading before switch %d: %v", step, err)
		}
		served = append(served, chunk[:n]...)
		if _, err := p.delivery.rate.Switch(ctx, ceiling); err != nil {
			t.Fatalf("switch %d to %s: %v", step, ceiling, err)
		}
		// Time for the first cut to reach the device before the next.
		rest := make([]byte, 1<<20)
		n, _ = io.ReadFull(resp.Body, rest)
		served = append(served, rest[:n]...)
	}
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	served = append(served, rest...)
	_, ffprobe := requireFFmpegTools(t)
	file := filepath.Join(t.TempDir(), "served.ts")
	if err := os.WriteFile(file, served, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, jump := bitrates(t, ffprobe, file); jump > 0.5 {
		t.Errorf("the timestamps jump %.2fs somewhere across two switches", jump)
	}
	out, err := exec.Command(strings.TrimSuffix(ffprobe, "ffprobe")+"ffmpeg", "-v", "error", "-i", file, "-f", "null", "-").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Errorf("decoding what was served: %v\n%s", err, out)
	}
}

// A change to what the stage makes already is no cut.
func TestAChangeToWhatIsAlreadyMadeCutsNothing(t *testing.T) {
	p, ctx := prepared(t, ac3Only())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.target.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadFull(resp.Body, make([]byte, 300<<10)); err != nil {
		t.Fatal(err)
	}
	chain, _ := p.delivery.rate.(*adjustable).chain()
	if _, err := chain.Switch(ctx, 1_200_000); err != nil {
		t.Fatal(err)
	}
	// Until the device has been given the cut, the stage that made the head is still the one making.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	cut := len(chain.retired)
	chain.mu.Unlock()
	if cut == 0 {
		t.Fatal("the first change was never cut in")
	}
	if _, err := chain.Switch(ctx, 1_200_000); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	pending := chain.next
	chain.mu.Unlock()
	if pending != nil {
		t.Error("a change to what the stage already makes cut another stage in")
	}
}

// A cut still waiting for the device is replaced by the next ask, and what the device is served stays one timeline.
func TestAnAskBeforeTheLastCutReachedTheDeviceReplacesIt(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveHeavy(t, ffmpegPath)
	c := realCast(&fakeDevice{caps: ac3Only()}, ffmpegPath, ffprobePath)
	program := programFromStream(t, origin.stream())
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	s := testPreparation(ctx, c, recovery.Attempt{Try: 1, Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})
	t.Cleanup(func() { _ = s.releases.release() })
	p, err := s.preparePresentation()
	if err != nil {
		t.Fatal(err)
	}
	chain, _ := p.delivery.rate.(*adjustable).chain()

	// The device has read nothing, so the first cut is still waiting when the second is asked for.
	if _, err := chain.Switch(ctx, 1_500_000); err != nil {
		t.Fatalf("first switch: %v", err)
	}
	chain.mu.Lock()
	first, firstAt := chain.next, chain.nextAt
	chain.mu.Unlock()
	if first == nil {
		t.Fatal("the first cut is not waiting for the device")
	}
	if _, err := chain.Switch(ctx, 900_000); err != nil {
		t.Fatalf("second switch: %v", err)
	}
	chain.mu.Lock()
	second, secondAt := chain.next, chain.nextAt
	chain.mu.Unlock()
	if second == first || secondAt > firstAt {
		t.Fatalf("the cut waiting is the first (%d) or later than it (%d > %d), want the second ask in its place", firstAt, secondAt, firstAt)
	}
	if !slices.Contains(chain.all(), first) {
		t.Error("the encoder the second ask replaced is not tracked, so nothing stops or reaps it")
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.target.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	served, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "served.ts")
	if err := os.WriteFile(file, served, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, jump := bitrates(t, ffprobePath, file); jump > 0.5 {
		t.Errorf("the timestamps jump %.2fs across the replaced cut", jump)
	}
}

// A delivery runs ahead of a device on a slow link by the lead of what the device takes, not of what the stream weighs.
func TestADeliveryRunsAheadOfASlowDeviceByWhatItTakesNotWhatTheStreamWeighs(t *testing.T) {
	p, ctx := prepared(t, ac3Only())
	chain, _ := p.delivery.rate.(*adjustable).chain()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.target.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// A device on a link far under the stream's 2.5 Mbit/s, reading 20 kB a second for longer than half a span.
	chunk := make([]byte, 2_000)
	for range takeSpan.Milliseconds() / 100 {
		if _, err := io.ReadFull(resp.Body, chunk); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	made := chain.Progress()
	weighed := int64(float64(made.Bytes) / made.Position.Seconds() * lead.Seconds())
	if weighed <= leadFloor {
		t.Skipf("the stream is too light (%d bytes of lead) for the floor to tell the two apart", weighed)
	}
	if got := chain.allowance(); got != leadFloor {
		t.Errorf("allowance = %d, want only the floor of %d for a device taking 20 kB/s, not the %d the stream weighs", got, leadFloor, weighed)
	}
}
