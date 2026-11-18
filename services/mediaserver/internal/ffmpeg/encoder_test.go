package ffmpeg

import (
	"context"
	"os/exec"
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestACancelledTestEncodeIsNotRemembered(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	proven := &provenEncoders{ffmpegPath: bin, slots: map[string]*proof{}}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if works, _ := proven.available(cancelled, libx264); works {
		t.Fatal("a cancelled test encode reported the encoder available")
	}
	works, ran := proven.available(t.Context(), libx264)
	if !works {
		t.Error("a cancelled test encode was cached as unavailable")
	}
	if !ran {
		t.Error("the first uncancelled ask did not run the test encode")
	}
	if _, ran := proven.available(t.Context(), libx264); ran {
		t.Error("a proven encoder was tested again")
	}
}

func TestASoftwareAskNeverGetsAHardwareEncoder(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	// Every test encode passes, so hardware would win wherever it is allowed.
	best := Encoders(bin, nil, PresetVeryfast)
	if e, _ := best(t.Context(), media.CodecH264, false); !e.Hardware {
		t.Fatalf("an ask allowing hardware got %s, want a hardware encoder", e.Name)
	}
	if e, ok := best(t.Context(), media.CodecH264, true); !ok || e.Hardware {
		t.Fatalf("a software ask got %s (hardware %t), want a software encoder", e.Name, e.Hardware)
	}
}

func TestADeclaredEncoderIsTriedFirst(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	// Every test encode passes, so the first candidate wins.
	if e, _ := Encoders(bin, []Declared{Declared(h264MediaCodec)}, PresetVeryfast)(t.Context(), media.CodecH264, false); e.Name != "h264_mediacodec" {
		t.Errorf("with h264_mediacodec declared, got %s", e.Name)
	}
	if e, _ := Encoders(bin, nil, PresetVeryfast)(t.Context(), media.CodecH264, false); e.Name == "h264_mediacodec" {
		t.Error("an encoder nobody declared was tried")
	}
}

func TestTheHostsPresetReachesOnlyTheEncodersThatTakeOne(t *testing.T) {
	flag := func(list []Encoder, name, flag string) string {
		e := list[slices.IndexFunc(list, func(e Encoder) bool { return e.Name == name })]
		at := slices.Index(e.Flags, flag)
		if at < 0 {
			return ""
		}
		return e.Flags[at+1]
	}
	got := candidates(nil, PresetSuperfast)
	for _, name := range []string{"libx264", "libx265"} {
		if preset := flag(got, name, "-preset"); preset != "superfast" {
			t.Errorf("%s encodes at %q, want superfast", name, preset)
		}
	}
	if pix := flag(got, "libx264", "-pix_fmt"); pix != "yuv420p" {
		t.Errorf("libx264 lost its own flags: pix_fmt %q", pix)
	}
	if preset := flag(got, "h264_nvenc", "-preset"); preset != "p4" {
		t.Errorf("a hardware encoder took the host's preset: %q", preset)
	}
	if preset := flag(encoders, "libx264", "-preset"); preset != "" {
		t.Errorf("the shared table was edited: libx264 is at %q", preset)
	}
}
