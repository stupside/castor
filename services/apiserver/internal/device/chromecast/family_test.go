package chromecast

import "testing"

func TestAPinnedAddressIsNamedAsDiscoveryNamesIt(t *testing.T) {
	for pinned, want := range map[string]string{
		"192.168.1.5:8009": "192.168.1.5",
		"192.168.1.5:8010": "192.168.1.5:8010",
		"192.168.1.5":      "192.168.1.5",
		"[fe80::1]:8009":   "fe80::1",
	} {
		if got, err := (Family{}).Locate(t.Context(), pinned); err != nil || got != want {
			t.Errorf("Locate(%q) = %q, %v; want %q", pinned, got, err, want)
		}
	}
}
