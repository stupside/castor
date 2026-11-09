package codec

import (
	"context"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestMeasuredAbsentTrackDoesNotRequestAnEncoderAtASplice(t *testing.T) {
	for _, tt := range []struct {
		name  string
		probe media.ProbeInfo
	}{
		{name: "audio only", probe: media.ProbeInfo{AudioCodec: media.CodecAAC}},
		{name: "video only", probe: media.ProbeInfo{VideoCodec: media.CodecH264}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			encoderCalls := 0
			in := Inputs{
				Probe: tt.probe, Measured: true, Spliced: true,
				Encoders: func(context.Context, media.Codec, bool) (ffmpeg.Encoder, bool) {
					encoderCalls++
					return ffmpeg.Encoder{}, false
				},
			}
			if tt.probe.VideoCodec == "" {
				video, reasons, err := floorVideoTrack(t.Context(), in)
				if err != nil || video.Action() != OmitTrack || len(reasons) != 0 || encoderCalls != 0 {
					t.Fatalf("absent video: track=%s reasons=%v encoder calls=%d err=%v", video.Name(), reasons, encoderCalls, err)
				}
			} else {
				audio, reasons := floorAudioTrack(in)
				if audio.Action() != OmitTrack || len(reasons) != 0 {
					t.Fatalf("absent audio: track=%s reasons=%v", audio.Name(), reasons)
				}
			}
		})
	}
}

func TestAnOmittedTrackIsNotReportedAsACopy(t *testing.T) {
	plan := Plan{Video: OmitVideo(), Audio: CopyAudio()}
	if got := plan.Copied(); got.Video || !got.Audio {
		t.Fatalf("copy evidence blames an omitted track: %+v", got)
	}
}

func TestUnidentifiedExistingTracksAreEncodedInsteadOfOmitted(t *testing.T) {
	in := Inputs{
		Probe: media.ProbeInfo{VideoPresent: true, AudioPresent: true}, Measured: true,
		Into: testFormat(t, media.MPEGTS), Encoders: hostEncoders,
		Caps: media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMPEGTS,
			Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC}}}},
	}
	for _, planner := range []func(context.Context, Inputs) (Plan, error){Floor, Served} {
		plan, err := planner(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Video.Action() != EncodeTrack || plan.Audio.Action() != EncodeTrack {
			t.Fatalf("existing unidentified track was dropped or copied: video=%s audio=%s", plan.Video.Name(), plan.Audio.Name())
		}
	}
}
