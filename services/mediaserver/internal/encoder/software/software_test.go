package software

import (
	"slices"
	"testing"
)

func TestTheCPUEncodersWorkAtTheHostsPreset(t *testing.T) {
	for _, preset := range []Preset{Ultrafast, Superfast, Veryfast} {
		encoders := Family{Preset: preset}.Encoders()
		if len(encoders) != 2 {
			t.Fatalf("the software family offers %d encoders, want libx264 and libx265", len(encoders))
		}
		for _, e := range encoders {
			at := slices.Index(e.Flags, "-preset")
			if at < 0 || e.Flags[at+1] != string(preset) {
				t.Errorf("%s at %v, want -preset %s", e.Name, e.Flags, preset)
			}
			if !slices.Contains(e.Flags, "yuv420p") {
				t.Errorf("%s lost its pixel format: %v", e.Name, e.Flags)
			}
			if e.Hardware || e.DeclaredOnly {
				t.Errorf("%s is %+v, want an encoder tried on its own on the CPU", e.Name, e)
			}
		}
	}
}
