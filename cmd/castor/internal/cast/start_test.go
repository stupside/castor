package cast

import (
	"testing"
	"time"
)

func TestParseStart(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":        0,
		"1h12m":   time.Hour + 12*time.Minute,
		"1:12":    time.Hour + 12*time.Minute,
		"1:12:30": time.Hour + 12*time.Minute + 30*time.Second,
		"90s":     90 * time.Second,
	} {
		if got, err := ParseStart(in); err != nil || got != want {
			t.Errorf("ParseStart(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"abc", "-5m", "1:2:3:4", "1:x", "1:75"} {
		if _, err := ParseStart(bad); err == nil {
			t.Errorf("ParseStart(%q) succeeded", bad)
		}
	}
}
