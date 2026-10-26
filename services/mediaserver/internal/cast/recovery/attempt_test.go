package recovery

import (
	"net/url"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// HLS pins the device to a rung; DASH lets the device choose.
func TestSelfFetchHeightIsWhatADeviceCouldChoose(t *testing.T) {
	ladder := source.Origin{Renditions: []source.Rendition{{Representation: "4k", Height: 2160}, {Representation: "hd", Height: 1080}}}
	if got := (Attempt{Origin: ladder, Rendition: source.Rendition{Representation: "hd", Height: 1080}}).SelfFetchHeight(); got != 2160 {
		t.Errorf("index-addressed SelfFetchHeight = %d, want the tallest rung 2160", got)
	}
	pinned := source.Rendition{URL: &url.URL{Path: "/1080.m3u8"}, Height: 1080}
	if got := (Attempt{Origin: ladder, Rendition: pinned}).SelfFetchHeight(); got != 1080 {
		t.Errorf("URL-addressed SelfFetchHeight = %d, want the pinned 1080", got)
	}
}

func TestAnAttemptBeginsWhereASourceHasAPlaceToBeginAt(t *testing.T) {
	vod := Attempt{Program: media.Program{Inputs: []media.Input{{}}}, Origin: source.Origin{Duration: time.Hour}}
	live := Attempt{Program: media.Program{Inputs: []media.Input{{Fetch: media.Fetch{Live: true}}}}}

	if _, ok := live.at(0); !ok {
		t.Error("a live attempt was refused its beginning")
	}
	if _, ok := live.at(time.Minute); ok {
		t.Error("a live source was given a position")
	}
	if got, ok := vod.at(time.Minute); !ok || got.Program.Start != time.Minute || got.Program.SelfFetchable() {
		t.Errorf("at = %+v, %t; want it read from 1m, never handed to a device that would start it over", got, ok)
	}
	if _, ok := vod.at(time.Hour); ok {
		t.Error("a source was given a position past its end")
	}
}
