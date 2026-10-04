package extract

import "testing"

func TestOnlyAFrameOnTheWebIsOpened(t *testing.T) {
	for src, want := range map[string]bool{
		"https://player.example/embed/1": true,
		"http://player.example/embed/1":  true,
		"javascript:alert(1)":            false,
		"data:text/html,<p>x</p>":        false,
		"file:///etc/passwd":             false,
		"chrome://settings":              false,
		"blob:https://site.example/id":   false,
	} {
		if got := webPage(src); got != want {
			t.Errorf("webPage(%q) = %v, want %v", src, got, want)
		}
	}
}
