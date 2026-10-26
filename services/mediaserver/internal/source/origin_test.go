package source

import (
	"cmp"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestARungIsChosenUnderTheHeightThenTheRateCeiling(t *testing.T) {
	rungs := []Rendition{
		{Representation: "1080", Height: 1080, Bitrate: 6_000_000},
		{Representation: "720", Height: 720, Bitrate: 3_000_000},
		{Representation: "480", Height: 480, Bitrate: 1_500_000},
	}
	richest := func(a, b Rendition) int { return cmp.Compare(a.Bitrate, b.Bitrate) }
	for _, tt := range []struct {
		name    string
		height  media.HeightCap
		ceiling media.Bitrate
		want    string
	}{
		{"no ceiling takes the richest the height admits", 1080, 0, "1080"},
		{"a ceiling takes the richest rung under it", 1080, 4_000_000, "720"},
		{"a ceiling at a rung's rate keeps that rung", 1080, 3_000_000, "720"},
		{"a ceiling under every rung takes the lightest, which the plan encodes down", 1080, 1_000_000, "480"},
		{"the height still bounds the choice under a ceiling", 480, 9_000_000, "480"},
		{"a height under every rung takes the shortest, whatever the ceiling", 240, 9_000_000, "480"},
	} {
		if got := (Origin{Renditions: rungs}).Choose(tt.height, tt.ceiling, richest); got.Representation != tt.want {
			t.Errorf("%s: chose %s, want %s", tt.name, got.Representation, tt.want)
		}
	}
}

func TestARungWithNoDeclaredRateIsNotKnownToOutweighACeiling(t *testing.T) {
	if !(Rendition{}).Within(1_000_000) {
		t.Error("a rung that declared no rate was ruled out by a ceiling, want it left to the plan, which measures it")
	}
	if (Rendition{Bitrate: 2_000_000}).Within(1_000_000) {
		t.Error("a rung declared over the ceiling was kept")
	}
}
