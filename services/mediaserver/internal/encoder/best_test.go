package encoder

import (
	"context"
	"os/exec"
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var (
	hardwareH264     = Encoder{Name: "h264_hardware", Codec: media.CodecH264, Hardware: true}
	softwareH264     = Encoder{Name: "h264_software", Codec: media.CodecH264}
	declaredOnlyH264 = Encoder{Name: "h264_declared_only", Codec: media.CodecH264, Hardware: true, DeclaredOnly: true}
)

// listed is a family offering exactly its encoders.
type listed []Encoder

func (l listed) Encoders() []Encoder { return slices.Clone(l) }

// passingFFmpeg is a binary every test encode passes with, so the first candidate wins.
func passingFFmpeg(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	return bin
}

func TestACancelledTestEncodeIsNotRemembered(t *testing.T) {
	proven := &provenEncoders{ffmpegPath: passingFFmpeg(t), slots: map[string]*proof{}}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if works, _ := proven.available(cancelled, softwareH264); works {
		t.Fatal("a cancelled test encode reported the encoder available")
	}
	works, ran := proven.available(t.Context(), softwareH264)
	if !works {
		t.Error("a cancelled test encode was cached as unavailable")
	}
	if !ran {
		t.Error("the first uncancelled ask did not run the test encode")
	}
	if _, ran := proven.available(t.Context(), softwareH264); ran {
		t.Error("a proven encoder was tested again")
	}
}

func TestASoftwareAskNeverGetsAHardwareEncoder(t *testing.T) {
	best := Best(passingFFmpeg(t), nil, listed{hardwareH264}, listed{softwareH264})
	if e, _ := best(t.Context(), media.CodecH264, false); !e.Hardware {
		t.Fatalf("an ask allowing hardware got %s, want a hardware encoder", e.Name)
	}
	if e, ok := best(t.Context(), media.CodecH264, true); !ok || e.Hardware {
		t.Fatalf("a software ask got %s (hardware %t), want a software encoder", e.Name, e.Hardware)
	}
}

func TestADeclaredEncoderIsTriedFirstAndAnotherNeverDeclaredIsNotTried(t *testing.T) {
	bin := passingFFmpeg(t)
	families := []Family{listed{declaredOnlyH264, hardwareH264}}
	if e, _ := Best(bin, []Encoder{declaredOnlyH264}, families...)(t.Context(), media.CodecH264, false); e.Name != declaredOnlyH264.Name {
		t.Errorf("with %s declared, got %s", declaredOnlyH264.Name, e.Name)
	}
	if e, _ := Best(bin, nil, families...)(t.Context(), media.CodecH264, false); e.Name != hardwareH264.Name {
		t.Errorf("with nothing declared, got %s, want the family's own hardware encoder", e.Name)
	}
}

func TestAnEncoderReadsALevelAsItsSpellingSaysAndAsGivenWhereItHasNone(t *testing.T) {
	spelled := Encoder{LevelSpelling: func(level string) string { return "level-" + level }}
	if got := spelled.Level("4.1"); got != "level-4.1" {
		t.Errorf("a spelled level is %q, want level-4.1", got)
	}
	if got := (Encoder{}).Level("4.1"); got != "4.1" {
		t.Errorf("a level with no spelling is %q, want 4.1", got)
	}
}
