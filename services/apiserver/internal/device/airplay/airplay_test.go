package airplay

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

func TestFeaturesReadTheLowWordWithoutItsLeadingZeros(t *testing.T) {
	for _, tc := range []struct {
		txt  string
		want features
	}{
		{"0x7F8AD0,0xB8BCF46", lgFeatures},
		{"0x007F8AD0,0x0B8BCF46", lgFeatures},
		{"0x5A7FFFF7,0x1E", 0x1E<<32 | 0x5A7FFFF7},
		{"0x77", 0x77},
	} {
		got, err := parseFeatures(tc.txt)
		if err != nil || got != tc.want {
			t.Errorf("parseFeatures(%q) = %#x, %v; want %#x", tc.txt, uint64(got), err, uint64(tc.want))
		}
	}
	if !features(lgFeatures).has(featureHomeKitPairing) || !features(lgFeatures).has(featureVideoV2) || features(lgFeatures).has(featureVideoV1) {
		t.Error("the LG's bits 46 and 49 are lost, or bit 0 invented")
	}
	if _, err := parseFeatures("0x1,0x2,0x3"); err == nil {
		t.Error("three words were accepted")
	}
}

func TestFexIsALittleEndianBitmap(t *testing.T) {
	// Bit 0 and bit 49, base64 without padding as receivers announce it.
	got, err := parseFeaturesEx("AQAAAAAAAg")
	if err != nil || got != 1|1<<49 {
		t.Errorf("fex = %#x, %v", uint64(got), err)
	}
	if got, err := parseFeaturesEx("AQAAAAAAAg=="); err != nil || got != 1|1<<49 {
		t.Errorf("padded fex = %#x, %v", uint64(got), err)
	}
}

func TestTheVideoMethodComesFromTheFeatures(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    features
		want method
	}{
		{"an LG with video v2 only", lgFeatures, methodVideoV2},
		{"an Apple TV with both prefers /play, as pyatv does", 1<<featureVideoV1 | 1<<featureVideoV2, methodVideoV1},
		{"an AirPlay 1 Apple TV", appleTVFeatures, methodVideoV1},
		{"an audio-only speaker", 1 << 9, methodNone},
	} {
		if got := tc.f.videoMethod(); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHLSIsQueuedAsStreamingAndTheRestAsAFile(t *testing.T) {
	u, _ := url.Parse("http://castor.example/stream.m3u8")
	hls := queueItem("ID", device.Playback{URL: u, Play: &mediav1.DeviceCommand_Play{Stream: &mediav1.DeviceCommand_Play_Playlist_{Playlist: &mediav1.DeviceCommand_Play_Playlist{}}}})
	if hls["mediaType"] != "streaming" || hls["timeToPausePlayback"] == nil || hls["Start-Position"] != nil {
		t.Errorf("HLS item = %v; a live playlist queued as a file stays loading", hls)
	}
	file := queueItem("ID", device.Playback{URL: u, Play: &mediav1.DeviceCommand_Play{Stream: &mediav1.DeviceCommand_Play_Progressive_{Progressive: &mediav1.DeviceCommand_Play_Progressive{Container: mediav1.Container_CONTAINER_MP4}}}})
	if file["mediaType"] != "file" || file["Start-Position"] == nil || file["Content-Location"] != u.String() {
		t.Errorf("MP4 item = %v", file)
	}
}

func pairedFamily(t *testing.T, r *fakeReceiver) Family {
	t.Helper()
	store := NewPairings(filepath.Join(t.TempDir(), "pairings.json"))
	if err := store.Save("dc:00:00:00:00:01", r.client); err != nil {
		t.Fatal(err)
	}
	return Family{Pairings: store}
}

func hlsPlayback() device.Playback {
	u, _ := url.Parse("http://castor.example:8410/cast/stream.m3u8")
	return device.Playback{URL: u, Play: &mediav1.DeviceCommand_Play{Stream: &mediav1.DeviceCommand_Play_Playlist_{Playlist: &mediav1.DeviceCommand_Play_Playlist{}}}}
}

func connect(t *testing.T, f Family, r *fakeReceiver) device.Device {
	t.Helper()
	dev, err := f.Connect(t.Context(), device.Info{Type: familyType, Address: r.address(), Name: "TV"})
	if err != nil {
		t.Fatal("connecting:", err)
	}
	t.Cleanup(func() { _ = dev.Close() })
	return dev
}

func awaitEnd(t *testing.T, dev device.Device) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := dev.AwaitEnd(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("AwaitEnd did not return once the receiver reported the end")
	}
	return err
}

func TestAVideoV2ReceiverPlaysTheQueuedItemToItsEnd(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	r.script = scenario(t, "file_plays_to_end")
	dev := connect(t, pairedFamily(t, r), r)
	if err := dev.Play(t.Context(), hlsPlayback()); err != nil {
		t.Fatal(err)
	}
	if err := awaitEnd(t, dev); err != nil {
		t.Fatalf("AwaitEnd = %v, want the item played to its end", err)
	}
	inserted := r.sent("insertPlayQueueItem")
	if len(inserted) != 1 {
		t.Fatalf("queued %d items", len(inserted))
	}
	item := inserted[0]["item"].(map[string]any)
	if item["mediaType"] != "streaming" || item["Content-Location"] != "http://castor.example:8410/cast/stream.m3u8" {
		t.Errorf("queued %v", item)
	}
	if end := r.sent("setProperty"); len(end) != 1 || end[0]["property"] != "actionAtItemEnd" {
		t.Errorf("setProperty = %v", end)
	}
	if !sawPrefix(r, "RECORD") {
		t.Error("the session was never recorded")
	}
	// The receiver reports 0 once stopped, which is no position of the item.
	if h, err := dev.Position(t.Context()); err == nil && h.At == 0 {
		t.Errorf("position after the end = %v, the stopped receiver's zero", h.At)
	}
}

func sawPrefix(r *fakeReceiver, prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, seen := range r.requests {
		if strings.HasPrefix(seen, prefix) {
			return true
		}
	}
	return false
}

func TestAPlayingItemReportsWhereItIs(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	r.script = scenario(t, "hls_plays")
	dev := connect(t, pairedFamily(t, r), r)
	if _, err := dev.Position(t.Context()); !errors.Is(err, device.ErrNoPosition) {
		t.Errorf("position before any Play = %v, want none", err)
	}
	if err := dev.Play(t.Context(), hlsPlayback()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		h, err := dev.Position(t.Context())
		if err == nil && h.At > 0 {
			if h.Seekable == nil || h.Seekable.End < 118*time.Second || h.Paused {
				t.Errorf("playhead = %+v", h)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no position from a playing item: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := dev.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	rates := r.sent("setRate")
	if last, _ := number(rates[len(rates)-1]["rate"]); last != 0 {
		t.Errorf("pause set rate %v", last)
	}
}

func TestAnItemTheReceiverCannotFetchFails(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	r.script = scenario(t, "refused")
	dev := connect(t, pairedFamily(t, r), r)
	// Play itself returns the refusal, which is what lets the media server serve the media instead.
	if err := dev.Play(t.Context(), hlsPlayback()); err == nil || !strings.Contains(err.Error(), "could not fetch") {
		t.Errorf("Play = %v, want the receiver asking castor to fetch for it", err)
	}
}

func TestAnItemTheReceiverGaveUpOnFails(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	// A receiver may only report the error, after giving up on its own.
	r.script = scenario(t, "refused")[1:]
	dev := connect(t, pairedFamily(t, r), r)
	if err := dev.Play(t.Context(), hlsPlayback()); err == nil || !strings.Contains(err.Error(), "-60009") {
		t.Errorf("Play = %v, want the receiver's error", err)
	}
}

func TestAReceiverThatNeverPlaysTheItemHasRefusedIt(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	// An LG handed a file it cannot fetch reports loading and nothing else.
	r.script = []map[string]any{{"type": "playbackState", "name": "loading"}}
	dev := connect(t, pairedFamily(t, r), r)
	defer func(was time.Duration) { verdictWithin = was }(verdictWithin)
	verdictWithin = 200 * time.Millisecond
	started := time.Now()
	if err := dev.Play(t.Context(), hlsPlayback()); !errors.Is(err, errRefused) || !strings.Contains(err.Error(), "did not begin playing") {
		t.Errorf("Play = %v, want a refusal", err)
	}
	// A refusal is the receiver's answer: castor does not hand the media over again on a new connection.
	if waited := time.Since(started); waited > 2*verdictWithin+time.Second {
		t.Errorf("Play waited %v for one verdict", waited)
	}
	if n := len(r.sent("insertPlayQueueItem")); n != 1 {
		t.Errorf("queued %d items for one Play", n)
	}
}

func TestAReceiverClosingTheSessionIsGone(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	r.script = scenario(t, "hls_plays")
	dev := connect(t, pairedFamily(t, r), r)
	if err := dev.Play(t.Context(), hlsPlayback()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	r.mu.Lock()
	_ = r.eventConn.Close()
	r.mu.Unlock()
	if _, gone := errors.AsType[*device.Gone](awaitEnd(t, dev)); !gone {
		t.Error("a closed event channel did not end the cast as gone")
	}
}

func TestStopLeavesTheReceiversPlayer(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	r.script = scenario(t, "hls_plays")
	dev := connect(t, pairedFamily(t, r), r)
	if err := dev.Play(t.Context(), hlsPlayback()); err != nil {
		t.Fatal(err)
	}
	if err := dev.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop left the session open, so the receiver keeps playing")
	}
	if err := awaitEnd(t, dev); err != nil {
		t.Errorf("AwaitEnd after Stop = %v", err)
	}
}

func TestAPlayAfterAnotherOpensANewSession(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	r.script = scenario(t, "hls_plays")
	dev := connect(t, pairedFamily(t, r), r)
	for range 2 {
		if err := dev.Play(t.Context(), hlsPlayback()); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.sent("insertPlayQueueItem")); n != 2 {
		t.Fatalf("queued %d items", n)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	verifies := 0
	for _, seen := range r.requests {
		if seen == "POST /pair-verify" {
			verifies++
		}
	}
	// A receiver queues a second item behind the first; only a new session plays it at once.
	if verifies != 4 {
		t.Errorf("pair-verify steps = %d, want two sessions", verifies)
	}
}

func TestAnUnpairedReceiverAsksForPairing(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	f := Family{Pairings: NewPairings(filepath.Join(t.TempDir(), "pairings.json"))}
	_, err := f.Connect(t.Context(), device.Info{Type: familyType, Address: r.address()})
	if !errors.Is(err, device.ErrPairingRequired) {
		t.Fatalf("Connect = %v, want pairing required", err)
	}
}

func TestAPairingTheReceiverForgotIsDropped(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	r.pin = "reject"
	f := pairedFamily(t, r)
	_, err := f.Connect(t.Context(), device.Info{Type: familyType, Address: r.address()})
	if !errors.Is(err, device.ErrPairingRequired) {
		t.Fatalf("Connect = %v, want pairing required", err)
	}
	if _, ok, _ := f.Pairings.Credentials("DC:00:00:00:00:01"); ok {
		t.Error("the rejected credentials were kept")
	}
}

func TestPairingWithThePINKeepsCredentialsThatVerify(t *testing.T) {
	r := newFakeReceiver(t, lgFeatures, lgStatus)
	r.pin = "0427"
	f := Family{Pairings: NewPairings(filepath.Join(t.TempDir(), "pairings.json"))}
	info := device.Info{Type: familyType, Address: r.address()}

	wrong, err := f.BeginPairing(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.Finish(t.Context(), "0428"); !errors.Is(err, device.ErrWrongPIN) {
		t.Fatalf("Finish with a wrong PIN = %v", err)
	}
	right, err := f.BeginPairing(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}
	if err := right.Finish(t.Context(), "0427"); err != nil {
		t.Fatal(err)
	}
	creds, ok, err := f.Pairings.Credentials("DC:00:00:00:00:01")
	if err != nil || !ok || string(creds.ReceiverID) != string(r.receiverID) {
		t.Fatalf("kept %v, %v, %v", creds, ok, err)
	}
	// The fake receiver verifies whoever it paired with last.
	r.client = creds
	r.pin = ""
	connect(t, f, r)
}

func TestTheStoreKeepsPairingsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "castor", "pairings.json")
	p := NewPairings(path)
	c := Credentials{ClientID: "c", ClientSeed: make([]byte, 32), ReceiverID: []byte("r"), ReceiverKey: make([]byte, 32)}
	if err := p.Save("aa:bb", c); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := p.Credentials("AA:BB"); err != nil || !ok || got.ClientID != "c" {
		t.Fatalf("Credentials = %v, %v, %v", got, ok, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("pairings file mode = %v, %v", info.Mode().Perm(), err)
		}
	}
	if strings.Contains(c.String(), "AAAA") || strings.Contains(c.String(), "seed") {
		t.Error("credentials print their keys")
	}
	if err := p.Forget("aa:bb"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.Credentials("AA:BB"); ok {
		t.Error("forgotten credentials remain")
	}
}

func TestAVideoV1AppleTVRetriesAnInternalErrorAndEndsWhenTheDurationGoes(t *testing.T) {
	r := newFakeReceiver(t, appleTVFeatures, 0)
	r.plays = []int{500}
	r.infos = []map[string]any{
		{},
		{"duration": 120.0, "position": 1.5, "rate": 1.0, "readyToPlay": true, "seekableTimeRanges": []any{map[string]any{"start": 0.0, "duration": 120.0}}},
		{"duration": 120.0, "position": 2.5, "rate": 1.0},
		{},
	}
	f := Family{Pairings: NewPairings(filepath.Join(t.TempDir(), "pairings.json"))}
	dev := connect(t, f, r)
	u, _ := url.Parse("http://castor.example:8410/cast/video.mp4")
	if err := dev.Play(t.Context(), device.Playback{URL: u, Play: &mediav1.DeviceCommand_Play{}}); err != nil {
		t.Fatal(err)
	}
	if err := dev.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !r.saw("POST /rate?value=0.000000") {
		t.Error("pause did not set the rate to zero")
	}
	if err := awaitEnd(t, dev); err != nil {
		t.Fatalf("AwaitEnd = %v", err)
	}
	plays := 0
	r.mu.Lock()
	for _, seen := range r.requests {
		if seen == "POST /play" {
			plays++
		}
	}
	r.mu.Unlock()
	if plays != 2 {
		t.Errorf("/play posted %d times, want once more after the 500", plays)
	}
}

func TestAVideoV1ErrorRefusesTheMedia(t *testing.T) {
	r := newFakeReceiver(t, appleTVFeatures, 0)
	r.infos = []map[string]any{{"error": map[string]any{"code": -11800, "domain": "AVFoundationErrorDomain"}}}
	dev := connect(t, Family{Pairings: NewPairings(filepath.Join(t.TempDir(), "p.json"))}, r)
	u, _ := url.Parse("http://castor.example:8410/cast/video.mp4")
	if err := dev.Play(t.Context(), device.Playback{URL: u, Play: &mediav1.DeviceCommand_Play{}}); err == nil || !strings.Contains(err.Error(), "-11800") {
		t.Errorf("Play = %v, want the Apple TV's error", err)
	}
}

func TestReceiversWithoutVideoAreRefused(t *testing.T) {
	r := newFakeReceiver(t, 1<<9, 0)
	_, err := Family{Pairings: NewPairings(filepath.Join(t.TempDir(), "p.json"))}.Connect(t.Context(), device.Info{Type: familyType, Address: r.address()})
	if !errors.Is(err, errNoVideo) {
		t.Errorf("Connect = %v", err)
	}
}
