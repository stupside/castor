package device

import "testing"

func TestWithPortCompletesOnlyABareHost(t *testing.T) {
	for address, want := range map[string]string{
		"192.168.1.4":      "192.168.1.4:1900",
		"192.168.1.4:8009": "192.168.1.4:8009",
		"tv.local":         "tv.local:1900",
		"fe80::1":          "[fe80::1]:1900",
		"[fe80::1]":        "[fe80::1]:1900",
		"[fe80::1]:8060":   "[fe80::1]:8060",
	} {
		if got := WithPort(address, "1900"); got != want {
			t.Errorf("WithPort(%q) = %q, want %q", address, got, want)
		}
	}
}
