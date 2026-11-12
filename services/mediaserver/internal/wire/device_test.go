package wire

import (
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"testing"
)

func h264Video() *mediav1.VideoSupport {
	return &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_H264{H264: &mediav1.H264Support{}}}
}

func TestCapabilitiesPreserveAtomicTransportAndTracks(t *testing.T) {
	caps := &mediav1.Capabilities{NativePlaybackTrial: true, Formats: []*mediav1.FormatSupport{
		{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: h264Video(), Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2}}}, Captions: []mediav1.CaptionFormat{mediav1.CaptionFormat_CAPTION_FORMAT_VTT}},
		{Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: &mediav1.FormatSupport_Audio{Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AC3}}},
		{Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{Subtitles: mediav1.HLSSupport_SUBTITLES_WEBVTT, ExtraSegmentFormats: []mediav1.HLSSupport_ExtraSegmentFormat{mediav1.HLSSupport_EXTRA_SEGMENT_FORMAT_TIKTIKPX, 999}, DolbyVisionEntry: true}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_Hevc{Hevc: &mediav1.HEVCSupport{BitDepths: []uint32{10}}}}}},
	}}
	got := FromCapabilities(caps)
	if !got.NativePlaybackTrial {
		t.Fatal("the receiver's native playback trial policy was lost")
	}
	if len(got.Formats) != 3 || got.Formats[0].Transport != media.TransportMP4 || got.Formats[1].Transport != media.TransportHLS || got.Formats[2].Transport != media.TransportHLSWebVTT {
		t.Fatalf("transport variants lost: %+v", got)
	}
	if got.Formats[0].Video.Codec != media.CodecH264 || got.Formats[0].Audio.Codec != media.CodecAAC || got.Formats[0].Audio.MaxChannels != 2 || len(got.Formats[0].Captions) != 1 {
		t.Fatalf("AV envelope lost: %+v", got.Formats[0])
	}
	if got.Formats[1].Video.Codec != "" || got.Formats[1].Audio.Codec != media.CodecAC3 || got.Formats[2].Audio.Codec != "" || got.Formats[2].Video.Codec != media.CodecHEVC {
		t.Fatalf("single-track variants widened: %+v", got)
	}
	if len(got.Formats[1].ExtraSegments) != 0 || len(got.Formats[2].ExtraSegments) != 1 || got.Formats[2].ExtraSegments[0] != media.HLSExtraSegmentTIKTIKPX {
		t.Fatalf("HLS segment support lost: %+v", got.Formats)
	}
	if got.Formats[1].DolbyVisionEntry || !got.Formats[2].DolbyVisionEntry {
		t.Fatalf("the dvh1 player signal is lost or widened: %+v", got.Formats)
	}
	if len(FromCapabilities(nil).Formats) != 0 {
		t.Fatal("absent capabilities widened")
	}
}

func TestCapabilitiesRejectUnknownVariantsWithoutWideningProfiles(t *testing.T) {
	for name, f := range map[string]*mediav1.FormatSupport{
		"unknown transport":     {Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: 999}}, Tracks: &mediav1.FormatSupport_Video{Video: h264Video()}},
		"unknown subtitle mode": {Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{Subtitles: 999}}, Tracks: &mediav1.FormatSupport_Video{Video: h264Video()}},
		"missing codec":         {Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{}}},
		"empty codec case":      {Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_H264{}}}},
		"unknown profile":       {Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_Hevc{Hevc: &mediav1.HEVCSupport{Profiles: []mediav1.HEVCSupport_Profile{999}}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if len(FromCapabilities(&mediav1.Capabilities{Formats: []*mediav1.FormatSupport{f}}).Formats) != 0 {
				t.Fatal("invalid envelope widened")
			}
		})
	}
}

func TestIncompleteAVEnvelopeIsNotWidenedToOneTrack(t *testing.T) {
	f := &mediav1.FormatSupport{
		Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}},
		Tracks:    &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: h264Video()}},
	}
	if got := FromCapabilities(&mediav1.Capabilities{Formats: []*mediav1.FormatSupport{f}}); len(got.Formats) != 0 {
		t.Fatalf("incomplete AV became usable: %+v", got)
	}
}
