package dlna

import (
	"testing"
	"time"
)

func TestParseClock(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0:05:42.035": 5*time.Minute + 42*time.Second + 35*time.Millisecond,
		"1:22:11":     time.Hour + 22*time.Minute + 11*time.Second,
	} {
		if got, err := parseClock(in); err != nil || got != want {
			t.Errorf("parseClock(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "NOT_IMPLEMENTED", "1:2", "NaN:00:00", "1e3:00:00", "0:61:00", "0:00:01.x"} {
		if _, err := parseClock(bad); err == nil {
			t.Errorf("parseClock(%q) succeeded", bad)
		}
	}
}
