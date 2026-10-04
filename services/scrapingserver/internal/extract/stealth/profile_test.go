package stealth

import (
	"strings"
	"testing"
)

func TestTheProfileSpeaksForTheBrowserThatIsRunning(t *testing.T) {
	p := New()
	if err := p.identify("HeadlessChrome/139.0.7258.66"); err != nil {
		t.Fatalf("identify: %v", err)
	}
	if !strings.Contains(p.userAgent, "Chrome/139.0.0.0 Safari/537.36") || strings.Contains(p.userAgent, "Headless") {
		t.Errorf("user agent = %q, want the running major version as a real Chrome reports it", p.userAgent)
	}
	if p.brands[2] != [2]string{"Google Chrome", "139"} || p.fullVersionList[2] != [2]string{"Google Chrome", "139.0.7258.66"} {
		t.Errorf("client hints = %v / %v, want the running version", p.brands, p.fullVersionList)
	}
	if err := New().identify("Chrome/beta"); err == nil {
		t.Error("identify accepted a product with no version")
	}
}
