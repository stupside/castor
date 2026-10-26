package source

import (
	"net/url"
	"testing"
)

func TestARungIsItselfWhetherItHasAURLOrNot(t *testing.T) {
	inManifest := Rendition{Representation: "720", Bitrate: 3_000_000}
	if !inManifest.Is(inManifest) {
		t.Error("a rung named inside its manifest is not itself")
	}
	addressed := Rendition{URL: &url.URL{Path: "/720.m3u8"}, Bitrate: 3_000_000}
	if !addressed.Is(Rendition{URL: &url.URL{Path: "/720.m3u8"}, Bitrate: 3_000_000}) {
		t.Error("two rungs at one URL are not one")
	}
	if addressed.Is(Rendition{Bitrate: 3_000_000}) || (Rendition{Bitrate: 3_000_000}).Is(addressed) {
		t.Error("a rung with a URL is one without")
	}
}
