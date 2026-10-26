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

// A rung the URL pins is what the device fetches; one the manifest addresses may be any of them.
func TestSelfFetchRateIsTheHeaviestADeviceCouldChoose(t *testing.T) {
	ladder := source.Origin{Renditions: []source.Rendition{{Representation: "4k", Bitrate: 12_000_000}, {Representation: "hd", Bitrate: 5_000_000}}}
	if got := (Attempt{Origin: ladder, Rendition: source.Rendition{Representation: "hd", Bitrate: 5_000_000}}).SelfFetchRate(); got != 12_000_000 {
		t.Errorf("index-addressed SelfFetchRate = %d, want the heaviest rung 12000000", got)
	}
	pinned := source.Rendition{URL: &url.URL{Path: "/hd.m3u8"}, Bitrate: 5_000_000}
	if got := (Attempt{Origin: ladder, Rendition: pinned}).SelfFetchRate(); got != 5_000_000 {
		t.Errorf("URL-addressed SelfFetchRate = %d, want the pinned 5000000", got)
	}
	if got := (Attempt{}).SelfFetchRate(); got != 0 {
		t.Errorf("SelfFetchRate of a source declaring none = %d, want 0", got)
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

func TestRuntimeIsWhatIsLeftFromWhereItBegins(t *testing.T) {
	a := Attempt{Origin: source.Origin{Duration: 2 * time.Hour}, Program: media.Program{Start: 30 * time.Minute}}
	if got := a.Runtime(); got != 90*time.Minute {
		t.Errorf("Runtime = %v, want 1h30m", got)
	}
	if got := (Attempt{Program: media.Program{Start: time.Hour}}).Runtime(); got != 0 {
		t.Errorf("Runtime without a declared duration = %v, want 0", got)
	}
}
