package source

import (
	"net/url"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestNarrowedRenditionDoesNotBorrowMasterObservation(t *testing.T) {
	stream := &Stream{
		URL:         &url.URL{Scheme: "https", Host: "origin.example", Path: "/master.m3u8"},
		ContentType: media.HLS,
		Probe:       &media.ProbeInfo{SourceSegments: media.HLSExtraSegmentTIKTIKPX},
	}
	published, err := ProgramFor(stream)
	if err != nil {
		t.Fatal(err)
	}
	observed := media.Observe(published, *stream.Probe)
	stream.Observation = &observed
	rung := &url.URL{Scheme: "https", Host: "origin.example", Path: "/video.m3u8"}
	program := media.Program{
		Inputs:     []media.Input{{ID: media.PrimaryInputID, URL: rung, ContentType: media.HLS}},
		Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}},
		ClockInput: media.PrimaryInputID,
		EndPolicy:  media.EndAtLongest,
	}
	narrowed, err := Narrowed(program, Rendition{Declared: &media.Declaration{VideoCodec: media.CodecH264}}, stream)
	if err != nil {
		t.Fatal(err)
	}
	if measured, ok := narrowed.Measurement(); ok {
		t.Fatalf("narrowed program borrowed the master's observation: %+v", measured)
	}
	declared, ok := narrowed.Declaration()
	if !ok || declared.VideoCodec != media.CodecH264 {
		t.Fatalf("narrowed declaration = %+v, declared=%t", declared, ok)
	}
}

func TestResolutionReusesOnlyTheObservedRendition(t *testing.T) {
	stream := &Stream{URL: &url.URL{Scheme: "https", Host: "origin.example", Path: "/master.m3u8"}, ContentType: media.HLS}
	selected, err := ProgramFor(stream)
	if err != nil {
		t.Fatal(err)
	}
	selected.Inputs[0].URL.Path = "/chosen.m3u8"
	info := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 720, SourceSegments: media.HLSExtraSegmentTIKTIKPX}
	observed := media.Observe(selected, info)
	stream.Observation, stream.Probe = &observed, &info
	master, err := ProgramFor(stream)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := master.Measurement(); ok {
		t.Fatalf("selected rendition's observation was assigned to the master: %+v", got)
	}
	resolved, err := Narrowed(selected, Rendition{}, stream)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := resolved.Measurement(); !ok || got.SourceSegments != media.HLSExtraSegmentTIKTIKPX || got.VideoHeight != 720 {
		t.Fatalf("matching selection lost its observation: %+v, observed=%t", got, ok)
	}
	selected.Inputs[0].URL.Path = "/replacement.m3u8"
	replacement, err := Narrowed(selected, Rendition{}, stream)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := replacement.Measurement(); ok {
		t.Fatalf("fresh manifest reused replaced rendition's observation: %+v", got)
	}
}
