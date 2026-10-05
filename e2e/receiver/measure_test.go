package receiver

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAnUnsuccessfulSilentDecoderCannotCertifyTheTape(t *testing.T) {
	failed, err := exec.LookPath("false")
	if err != nil {
		t.Skip("requires a silent failing executable")
	}
	errors, _ := decode(t.Context(), failed, filepath.Join(t.TempDir(), "tape"), false)
	if len(errors) == 0 {
		t.Fatal("a decoder that exited unsuccessfully without diagnostics certified the tape as clean")
	}
}

func TestAFailedKeyframeProbeCannotCertifyTheHeightCeiling(t *testing.T) {
	failed, err := exec.LookPath("false")
	if err != nil {
		t.Skip("requires a silent failing executable")
	}
	if _, err := tallestKeyframe(t.Context(), failed, "tape"); err == nil {
		t.Fatal("an unsuccessful probe certified the picture height")
	}
}
