package vaapi

import (
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestTheDefaultRenderNodeComesFirstAndOnlyOnce(t *testing.T) {
	var h264 []string
	for _, e := range encodersOnNodes([]string{"/dev/dri/renderD129", "/dev/dri/renderD128"}) {
		if e.Codec == media.CodecH264 {
			h264 = append(h264, e.InitArgs[1])
		}
	}
	want := []string{"vaapi=va:/dev/dri/renderD128", "vaapi=va:/dev/dri/renderD129"}
	if !slices.Equal(h264, want) {
		t.Errorf("H.264 is tried on %v, want %v", h264, want)
	}
}
