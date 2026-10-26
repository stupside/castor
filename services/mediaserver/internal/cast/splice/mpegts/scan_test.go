package mpegts

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
)

const (
	mapStream   = 0x100
	videoStream = 0x101
	audioStream = 0x102
)

func packet(header []byte, payload ...byte) []byte {
	pkt := bytes.Repeat([]byte{0xff}, packetSize)
	n := copy(pkt, header)
	copy(pkt[n:], payload)
	return pkt
}

func pat() []byte {
	return packet([]byte{syncByte, 0x40, 0x00, 0x10},
		0x00, 0x00, 0xb0, 0x0d, 0x00, 0x01, 0xc1, 0x00, 0x00, 0x00, 0x01, 0xe1, 0x00, 0, 0, 0, 0)
}

func pmt() []byte {
	return packet([]byte{syncByte, 0x40 | mapStream>>8, mapStream & 0xff, 0x10},
		0x00, 0x02, 0xb0, 0x17, 0x00, 0x01, 0xc1, 0x00, 0x00, 0xe1, 0x01, 0xf0, 0x00,
		0x1b, 0xe0|videoStream>>8, videoStream&0xff, 0xf0, 0x00,
		0x0f, 0xe0|audioStream>>8, audioStream&0xff, 0xf0, 0x00, 0, 0, 0, 0)
}

func pes(pts time.Duration) []byte {
	t := int64(pts * 90000 / time.Second)
	return []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 0x05,
		0x21 | byte(t>>30&7)<<1, byte(t >> 22), byte(t>>15&0x7f)<<1 | 1, byte(t >> 7), byte(t&0x7f)<<1 | 1}
}

// frame is a video packet opening a picture at pts; a keyframe marks its adaptation field as a random access point.
func frame(pts time.Duration, key bool) []byte {
	if key {
		return packet([]byte{syncByte, 0x40 | videoStream>>8, videoStream & 0xff, 0x30, 0x01, 0x40}, pes(pts)...)
	}
	return packet([]byte{syncByte, 0x40 | videoStream>>8, videoStream & 0xff, 0x10}, pes(pts)...)
}

func sound() []byte {
	return packet([]byte{syncByte, 0x40 | audioStream>>8, audioStream & 0xff, 0x10}, 0, 0, 1, 0xc0)
}

// stream is two seconds of pictures, a keyframe each second, and the offset of each keyframe.
func stream() (data []byte, keys []int64) {
	data = append(data, pat()...)
	data = append(data, pmt()...)
	for i := range 6 {
		pts := time.Duration(i) * 500 * time.Millisecond
		if i%3 == 0 {
			data = append(data, pat()...)
			data = append(data, pmt()...)
			keys = append(keys, int64(len(data)))
		}
		data = append(data, frame(pts, i%3 == 0)...)
		data = append(data, sound()...)
	}
	return data, keys
}

func TestTheKeyframesOfAStreamAreListedWithWhereAndWhen(t *testing.T) {
	data, keys := stream()
	x := splice.NewIndex(Framing{})
	if _, err := x.Write(data); err != nil {
		t.Fatal(err)
	}
	first, ok := x.After(0)
	if !ok || first.Offset != keys[0] || first.PTS != 0 {
		t.Fatalf("first cut = %+v, want offset %d at 0s", first, keys[0])
	}
	second, ok := x.After(first.Offset + 1)
	if !ok || second.Offset != keys[1] || second.PTS != 1500*time.Millisecond {
		t.Fatalf("second cut = %+v, want offset %d at 1.5s", second, keys[1])
	}
	if !bytes.Equal(second.Tables, slices.Concat(pat(), pmt())) {
		t.Errorf("the cut carries %d bytes of tables, want the PAT and PMT in force", len(second.Tables))
	}
	if _, ok := x.After(keys[1] + 1); ok {
		t.Error("a cut was listed past the last keyframe")
	}
}

func TestTheSameCutsAreListedHoweverTheStreamIsWritten(t *testing.T) {
	data, keys := stream()
	x := splice.NewIndex(Framing{})
	for i := range data {
		if _, err := x.Write(data[i : i+1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range keys {
		cut, ok := x.After(want)
		if !ok || cut.Offset != want {
			t.Fatalf("cut after %d = %+v, want one at %d", want, cut, want)
		}
	}
}

func TestNoiseBeforeTheStreamIsSkipped(t *testing.T) {
	data, keys := stream()
	x := splice.NewIndex(Framing{})
	noise := []byte{1, 2, 3, 4, 5}
	_, _ = x.Write(append(noise, data...))
	cut, ok := x.After(0)
	if !ok || cut.Offset != keys[0]+int64(len(noise)) {
		t.Fatalf("cut = %+v, want the first keyframe past the noise at %d", cut, keys[0]+int64(len(noise)))
	}
}

func TestAKeyframeBeforeTheTablesIsNoPlaceToCut(t *testing.T) {
	x := splice.NewIndex(Framing{})
	_, _ = x.Write(frame(0, true))
	if _, ok := x.After(0); ok {
		t.Error("a cut was listed before the stream said what its streams are")
	}
}

func TestWaitingForACutEndsWhenOneIsWrittenOrTheStreamEnds(t *testing.T) {
	data, keys := stream()
	x := splice.NewIndex(Framing{})
	got := make(chan splice.Cut, 1)
	go func() {
		cut, err := x.Wait(t.Context(), keys[1])
		if err != nil {
			t.Error(err)
		}
		got <- cut
	}()
	_, _ = x.Write(data[:keys[1]])
	select {
	case cut := <-got:
		t.Fatalf("waited cut = %+v before it was written", cut)
	case <-time.After(50 * time.Millisecond):
	}
	_, _ = x.Write(data[keys[1]:])
	if cut := <-got; cut.Offset != keys[1] {
		t.Errorf("waited cut at %d, want %d", cut.Offset, keys[1])
	}

	x.Close()
	if _, err := x.Wait(t.Context(), keys[1]+1); !errors.Is(err, splice.ErrNoCut) {
		t.Errorf("Wait past the last cut of a closed stream = %v, want splice.ErrNoCut", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := splice.NewIndex(Framing{}).Wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait on a cancelled context = %v", err)
	}
}

func TestTheCutAtATimeIsFoundWithinATolerance(t *testing.T) {
	data, keys := stream()
	x := splice.NewIndex(Framing{})
	if _, err := x.Write(data); err != nil {
		t.Fatal(err)
	}
	cut, err := x.Matching(t.Context(), 1500*time.Millisecond+5*time.Millisecond, 20*time.Millisecond)
	if err != nil || cut.Offset != keys[1] {
		t.Fatalf("cut = %+v, %v, want the keyframe at 1.5s (offset %d)", cut, err, keys[1])
	}
	if _, err := x.Matching(t.Context(), 1000*time.Millisecond, 20*time.Millisecond); !errors.Is(err, splice.ErrNoMatch) {
		t.Errorf("a time between keyframes found %v, want splice.ErrNoMatch", err)
	}
	x.Close()
	if _, err := x.Matching(t.Context(), 5*time.Second, 20*time.Millisecond); !errors.Is(err, splice.ErrNoCut) {
		t.Errorf("a time the stream ended before found %v, want splice.ErrNoCut", err)
	}
}

func TestTheCutAtATimeTheStreamHasNotReachedIsWaitedFor(t *testing.T) {
	data, _ := stream()
	x := splice.NewIndex(Framing{})
	found := make(chan error, 1)
	go func() {
		_, err := x.Matching(t.Context(), 1500*time.Millisecond, 20*time.Millisecond)
		found <- err
	}()
	if _, err := x.Write(data[:len(data)/3]); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-found:
		t.Fatalf("matched before the stream reached it: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := x.Write(data[len(data)/3:]); err != nil {
		t.Fatal(err)
	}
	if err := <-found; err != nil {
		t.Errorf("match = %v", err)
	}
}

func TestTheMediaAStreamHoldsIsCountedFromItsFirstKeyframeToItsLastPicture(t *testing.T) {
	data, _ := stream()
	x := splice.NewIndex(Framing{})
	if got := x.Extent(); got != 0 {
		t.Errorf("an empty stream holds %v", got)
	}
	if _, err := x.Write(data); err != nil {
		t.Fatal(err)
	}
	if got := x.Extent(); got != 2500*time.Millisecond {
		t.Errorf("extent = %v, want 2.5s: six pictures half a second apart", got)
	}
}

// soundAt is a frame of sound opening at pts.
func soundAt(pts time.Duration) []byte {
	t := int64(pts * 90000 / time.Second)
	return packet([]byte{syncByte, 0x40 | audioStream>>8, audioStream & 0xff, 0x10},
		0, 0, 1, 0xc0, 0, 0, 0x80, 0x80, 0x05,
		0x21|byte(t>>30&7)<<1, byte(t>>22), byte(t>>15&0x7f)<<1|1, byte(t>>7), byte(t&0x7f)<<1|1)
}

// timedStream is a keyframe a second and a frame of sound every 250ms, the sound a little ahead of the pictures as a muxer writes it.
func timedStream() (data []byte, keys []splice.Cut) {
	data = append(data, pat()...)
	data = append(data, pmt()...)
	for i := range 12 {
		pts := time.Duration(i) * 250 * time.Millisecond
		data = append(data, soundAt(pts)...)
		if i%4 == 0 {
			data = append(data, pat()...)
			data = append(data, pmt()...)
			keys = append(keys, splice.Cut{Offset: int64(len(data)), PTS: pts})
		}
		data = append(data, frame(pts, i%4 == 0)...)
	}
	return data, keys
}

func TestTheSoundAStreamHasAfterAPlaceIsFound(t *testing.T) {
	data, keys := timedStream()
	x := splice.NewIndex(Framing{})
	if _, err := x.Write(data); err != nil {
		t.Fatal(err)
	}
	// The sound is written a frame ahead of each picture, so the first to open after the keyframe at 1s is the one at 1.25s.
	got, ok := x.SoundAfter(keys[1].Offset)
	if !ok || got != 1250*time.Millisecond {
		t.Errorf("sound after the keyframe at 1s opens at %v (%t), want 1.25s", got, ok)
	}
	if _, ok := x.SoundAfter(int64(len(data))); ok {
		t.Error("sound was found past the end of the stream")
	}
}
