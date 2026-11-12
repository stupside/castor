package hls

import (
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestADolbyVisionEntryIsNamedByItsProfileAndLevel(t *testing.T) {
	entry := mp4.CreateVisualSampleEntryBox("dvh1", 3840, 1608, nil)
	entry.AddChild(mp4.CreateDoViConfigurationBox(1, 0, 8, 6, true, false, true, 1))
	stsd := &mp4.StsdBox{}
	stsd.AddChild(entry)
	moov := &mp4.MoovBox{Traks: []*mp4.TrakBox{{Mdia: &mp4.MdiaBox{Minf: &mp4.MinfBox{Stbl: &mp4.StblBox{Stsd: stsd}}}}}}
	// The CODECS a player reading the dvh1 entry checks, as an LG over AirPlay showed Dolby Vision with.
	if got, err := mediaCodecs(moov); err != nil || got != "dvh1.08.06" {
		t.Errorf("CODECS = %q, %v, want dvh1.08.06", got, err)
	}
}

func TestOnlyAPlayerThatAsksMarksDolbyVisionDvh1(t *testing.T) {
	for _, dvh1 := range []bool{false, true} {
		plan, ok := Adapter{}.Select(media.FormatSupport{Transport: media.TransportHLS, DolbyVisionEntry: dvh1}, nil, 0)
		if !ok {
			t.Fatal("HLS was not selected")
		}
		if got := plan.(hlsPlan).Format().DolbyVisionEntry; got != dvh1 {
			t.Errorf("format DolbyVisionEntry = %v for a player asking %v", got, dvh1)
		}
	}
}
