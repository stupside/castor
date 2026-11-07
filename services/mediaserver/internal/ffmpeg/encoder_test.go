package ffmpeg

import (
	"context"
	"os/exec"
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// desktopCandidates is what castor tries on a host that declares nothing, before any VA-API node it finds.
var desktopCandidates = []string{
	"h264_videotoolbox", "h264_nvenc", "h264_qsv", "h264_vaapi", "h264_amf", "libx264",
	"hevc_videotoolbox", "hevc_nvenc", "hevc_qsv", "hevc_vaapi", "hevc_amf", "libx265",
}

func names(encoders []Encoder) []string {
	out := make([]string, len(encoders))
	for i, e := range encoders {
		out[i] = e.Name
	}
	return out
}

func TestAHostThatDeclaresNothingTriesWhatItAlwaysDid(t *testing.T) {
	c, err := NewCatalog("ffmpeg", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(candidates(c.proven.declared))[:len(desktopCandidates)]; !slices.Equal(got, desktopCandidates) {
		t.Fatalf("candidates = %v, want %v", got, desktopCandidates)
	}
	if libx264.GOPKeyframes || libx264.AverageRate || libx264.Level != nil {
		t.Fatal("the software encoder must keep forced keyframes, its VBV and castor's level spelling")
	}
}

func TestDeclaredEncodersAreTriedFirstAndUnknownOnesRefused(t *testing.T) {
	c, err := NewCatalog("ffmpeg", []string{"hevc_mediacodec", "h264_mediacodec"})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(candidates(c.proven.declared))[:len(desktopCandidates)+2]; !slices.Equal(got, slices.Concat([]string{"hevc_mediacodec", "h264_mediacodec"}, desktopCandidates)) {
		t.Fatalf("candidates = %v", got)
	}
	if _, err := NewCatalog("ffmpeg", []string{"h264_imaginary"}); err == nil {
		t.Fatal("an encoder castor knows nothing of must be refused, not ignored")
	}
}

func TestMediaCodecLevelsAreItsNamedConstants(t *testing.T) {
	h264, hevc := declarable["h264_mediacodec"], declarable["hevc_mediacodec"]
	for _, tt := range []struct {
		encoder     Encoder
		level, want string
	}{
		// "4.0" would parse as the number 4, which MediaCodec reads as level 1.1.
		{h264, "4.0", "4"}, {h264, "4.2", "4.2"}, {h264, "3.1", "3.1"},
		{hevc, "4.0", "m4"}, {hevc, "4.1", "m4.1"}, {hevc, "5.0", "m5"},
	} {
		if got := tt.encoder.Level(tt.level); got != tt.want {
			t.Errorf("%s level %q = %q, want %q", tt.encoder.Name, tt.level, got, tt.want)
		}
	}
	for _, e := range []Encoder{h264, hevc} {
		if !e.Hardware || !e.GOPKeyframes || !e.AverageRate || !slices.Contains(e.Flags, "nv12") {
			t.Errorf("%s: %+v", e.Name, e)
		}
	}
}

func TestARetiredEncoderIsNoLongerOffered(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	// Every test encode succeeds, so the declared hardware encoder wins until it is retired.
	c, err := NewCatalog(bin, []string{"h264_mediacodec"})
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := c.Best(t.Context(), media.CodecH264); e.Name != "h264_mediacodec" {
		t.Fatalf("Best = %s, want the declared hardware encoder", e.Name)
	}
	if e, _ := c.Software(t.Context(), media.CodecH264); e.Name != "libx264" {
		t.Fatalf("Software = %s, want libx264", e.Name)
	}
	c.Retire("h264_mediacodec")
	if e, _ := c.Best(t.Context(), media.CodecH264); e.Name == "h264_mediacodec" {
		t.Fatal("a retired encoder was offered again")
	}
}

func TestACancelledTestEncodeIsNotRemembered(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	proven := &provenEncoders{ffmpegPath: bin, slots: map[string]*proof{}, retired: map[string]bool{}}
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
