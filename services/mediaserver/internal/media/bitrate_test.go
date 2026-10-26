package media

import "testing"

func TestACeilingAdmitsOnlyARateProvenUnderIt(t *testing.T) {
	for _, tt := range []struct {
		name          string
		ceiling, rate Bitrate
		want          bool
	}{
		{"no ceiling admits any rate", 0, 9_000_000, true},
		{"no ceiling admits an unknown rate", 0, 0, true},
		{"a rate under the ceiling fits", 4_000_000, 3_000_000, true},
		{"a rate at the ceiling fits", 4_000_000, 4_000_000, true},
		{"a rate over the ceiling does not", 4_000_000, 4_000_001, false},
		{"an unknown rate is not proven to fit", 4_000_000, 0, false},
	} {
		if got := tt.ceiling.Admits(tt.rate); got != tt.want {
			t.Errorf("%s: Admits = %t, want %t", tt.name, got, tt.want)
		}
	}
}

func TestARateRendersForFFmpegAndForPeople(t *testing.T) {
	if got := Bitrate(2_345_678).FFmpeg(); got != "2345k" {
		t.Errorf("FFmpeg = %q, want 2345k", got)
	}
	if got := Bitrate(2_345_678).String(); got != "2.3 Mbit/s" {
		t.Errorf("String = %q, want 2.3 Mbit/s", got)
	}
}
