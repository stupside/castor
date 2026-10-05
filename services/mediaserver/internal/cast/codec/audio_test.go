package codec

import (
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestSurroundAudioTargets(t *testing.T) {
	aacStereo := media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}
	ac3 := media.AudioSupport{Codec: media.CodecAC3}
	eac3 := media.AudioSupport{Codec: media.CodecEAC3}
	for _, tt := range []struct {
		name     string
		caps     []media.AudioSupport
		codec    media.Codec
		channels int
		want     string
		wantCh   int
	}{
		{"5.1 ac3 the device decodes is copied", []media.AudioSupport{aacStereo, ac3}, media.CodecAC3, 6, "copy", 0},
		{"5.1 aac keeps its layout in ac3", []media.AudioSupport{aacStereo, ac3}, media.CodecAAC, 6, "ac3", 6},
		{"eac3 is preferred when advertised", []media.AudioSupport{eac3, ac3}, "dts", 6, "eac3", 6},
		{"7.1 folds to the 5.1 ceiling", []media.AudioSupport{eac3}, "dts", 8, "eac3", 6},
		{"no surround support downmixes to stereo aac", []media.AudioSupport{aacStereo}, "dts", 6, "aac", 2},
		{"surround codec advertised only in stereo is downmixed", []media.AudioSupport{{Codec: media.CodecEAC3, MaxChannels: 2}}, "dts", 6, "eac3", 2},
		{"mono-only audio support is respected", []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 1}}, "dts", 6, "aac", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			track := mustDecideAudio(t, Inputs{
				Caps:     media.Capabilities{Audio: tt.caps},
				Probe:    media.ProbeInfo{AudioCodec: tt.codec, AudioChannels: tt.channels},
				Measured: true, Into: testFormat(t, media.MP4),
			})
			enc, _ := track.Encode()
			if track.Name() != tt.want || enc.Channels != tt.wantCh {
				t.Errorf("audio = %s/%dch, want %s/%dch", track.Name(), enc.Channels, tt.want, tt.wantCh)
			}
		})
	}
}

func TestAnUnmeasuredAudioTrackIsNotAnAbsentOne(t *testing.T) {
	caps := media.Capabilities{Audio: []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}}}
	in := Inputs{Caps: caps, Into: testFormat(t, media.MP4), Measured: true}
	if track := mustDecideAudio(t, in); track.Name() != "copy" {
		t.Errorf("a probe that measured no audio decided %q, want copy", track.Name())
	}
	in.Measured = false
	if enc, ok := mustDecideAudio(t, in).Encode(); !ok || enc.Codec != media.CodecAAC || enc.Channels != 2 {
		t.Errorf("an unmeasured audio track decided %+v, want the stereo AAC floor", enc)
	}
}

func mustDecideAudio(t *testing.T, in Inputs) Track[AudioEncode] {
	t.Helper()
	track, _, err := decideAudio(in)
	if err != nil {
		t.Fatalf("decideAudio: %v", err)
	}
	return track
}

func testFormat(t *testing.T, contentType string) container.Format {
	t.Helper()
	f, ok := container.For(contentType)
	if !ok {
		t.Fatalf("no producible format for %q", contentType)
	}
	return f
}
