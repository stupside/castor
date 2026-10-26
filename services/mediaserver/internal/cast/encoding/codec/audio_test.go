package codec

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
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
			var formats []media.FormatSupport
			for _, audio := range tt.caps {
				formats = append(formats, media.FormatSupport{Transport: media.TransportMP4, Audio: audio})
			}
			track := mustDecideAudio(t, Inputs{
				Caps:     media.Capabilities{Formats: formats},
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
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	in := Inputs{Caps: caps, Into: testFormat(t, media.MP4), Measured: true}
	if track := mustDecideAudio(t, in); track.Action() != OmitTrack {
		t.Errorf("a probe that measured no audio decided %q, want omission", track.Name())
	}
	in.Measured = false
	if enc, ok := mustDecideAudio(t, in).Encode(); !ok || enc.Codec != media.CodecAAC || enc.Channels != 2 {
		t.Errorf("an unmeasured audio track decided %+v, want the stereo AAC floor", enc)
	}
}

func TestARateCeilingBoundsThePictureAndTheSound(t *testing.T) {
	h264 := media.VideoSupport{Codec: media.CodecH264}
	decodes := func(codecs ...media.Codec) []media.FormatSupport {
		var formats []media.FormatSupport
		for _, c := range codecs {
			audio := media.AudioSupport{Codec: c}
			if c == media.CodecAAC {
				audio.MaxChannels = 2
			}
			formats = append(formats, media.FormatSupport{Transport: media.TransportMPEGTS, Video: h264, Audio: audio})
		}
		return formats
	}
	surround := media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHeight: 1080, AudioCodec: media.CodecAC3, AudioChannels: 6}
	tall := surround
	tall.VideoHeight = 2160
	mp3 := surround
	mp3.AudioCodec, mp3.AudioChannels = media.CodecMP3, 2
	aac51 := surround
	aac51.AudioCodec = media.CodecAAC
	stereo := surround
	stereo.AudioCodec, stereo.AudioChannels = media.CodecAAC, 2
	sd := stereo
	sd.VideoHeight, sd.VideoLevel = 480, 30
	soundOnly := media.ProbeInfo{AudioCodec: media.CodecAAC, AudioChannels: 2}
	level30 := []media.FormatSupport{{Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264, MaxLevel: 30}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}

	for _, tt := range []struct {
		name                  string
		formats               []media.FormatSupport
		probe                 media.ProbeInfo
		ceiling, rate, weighs media.Bitrate
		want                  string
		reasons               []Reason
	}{
		{"no ceiling copies the sound beside an encoded picture", decodes(media.CodecAC3), tall, 0, 6_500_000, 0,
			"4000k + copy", []Reason{reasonHeightLimit}},
		{"a whole copy proven under the ceiling keeps its sound", decodes(media.CodecAC3), surround, 3_000_000, 2_500_000, 0,
			"copy + copy", nil},
		{"a sound nobody weighed is encoded and the picture leaves it room", decodes(media.CodecAC3), surround, 3_000_000, 6_500_000, 0,
			"2402k + ac3/6@448k", []Reason{reasonBitrateLimit, reasonBitrateLimit}},
		{"a sound weighed light enough is kept and the picture takes what it leaves", decodes(media.CodecAC3), surround, 3_000_000, 6_500_000, 384_000,
			"2466k + copy", []Reason{reasonBitrateLimit}},
		{"a sound weighed too heavy for its share is encoded under it", decodes(media.CodecAC3), surround, 1_000_000, 6_500_000, 640_000,
			"502k + ac3/6@448k", []Reason{reasonBitrateLimit, reasonBitrateLimit}},
		{"a sound is not encoded heavier than its weighed source, even for its channels", decodes(media.CodecAC3, media.CodecAAC), aac51, 3_000_000, 6_500_000, 320_000,
			"2594k + aac/2@256k", []Reason{reasonBitrateLimit, reasonDeviceAudio}},
		{"an unweighed surround sound keeps its channels where the ceiling leaves room", decodes(media.CodecAAC, media.CodecAC3), aac51, 3_000_000, 6_500_000, 0,
			"2402k + ac3/6@448k", []Reason{reasonBitrateLimit, reasonDeviceAudio, reasonBitrateLimit}},
		{"of two surround sounds the lighter is taken", decodes(media.CodecAC3, media.CodecEAC3), surround, 3_000_000, 6_500_000, 0,
			"2466k + eac3/6@384k", []Reason{reasonBitrateLimit, reasonDeviceAudio, reasonBitrateLimit}},
		{"a sound too heavy for its share gives way to one that fits", decodes(media.CodecAC3, media.CodecAAC), surround, 950_000, 6_500_000, 0,
			"646k + aac/2@256k", []Reason{reasonBitrateLimit, reasonDeviceAudio, reasonBitrateLimit}},
		{"a ceiling under the least picture and sound floors the picture and says so", decodes(media.CodecAAC), surround, 600_000, 6_500_000, 0,
			"500k + aac/2@256k", []Reason{reasonBitrateLimit, reasonBitrateFloor, reasonDeviceAudio, reasonBitrateLimit}},
		{"a sound castor cannot encode stays a copy, is reserved the lightest weight, and says so", decodes(media.CodecMP3), mp3, 3_000_000, 6_500_000, 0,
			"2594k + copy", []Reason{reasonBitrateLimit, reasonBitrateFloor}},
		{"a picture proven under the ceiling is encoded once a heavier sound would take it over", decodes(media.CodecAC3), stereo, 3_000_000, 2_900_000, 128_000,
			"2402k + ac3/2@448k", []Reason{reasonBitrateLimit, reasonDeviceAudio}},
		{"a picture castor cannot encode stays a copy under the ceiling and says so", level30, sd, 3_000_000, 6_500_000, 128_000,
			"copy + copy", []Reason{reasonBitrateFloor}},
		{"a sound alone keeps no room for a picture it does not have", decodes(media.CodecAAC), soundOnly, 600_000, 0, 320_000,
			"omit + copy", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			probe := tt.probe
			probe.AudioBitrate = tt.weighs
			plan, err := Served(t.Context(), Inputs{
				Caps: media.Capabilities{Formats: tt.formats}, Probe: probe, Measured: true, MaxHeight: 1080,
				MaxBitrate: tt.ceiling, Rate: tt.rate, Into: testFormat(t, media.MPEGTS), Encoders: hostEncoders,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := cast(plan); got != tt.want {
				t.Errorf("cast = %s, want %s", got, tt.want)
			}
			if !slices.Equal(plan.Refusals, tt.reasons) {
				t.Errorf("reasons = %v, want %v", plan.Refusals, tt.reasons)
			}
		})
	}
}

func TestACeilingThatTakesNothingFromThePictureChangesNoPlan(t *testing.T) {
	h264 := media.VideoSupport{Codec: media.CodecH264}
	ac3 := media.FormatSupport{Transport: media.TransportMPEGTS, Video: h264, Audio: media.AudioSupport{Codec: media.CodecAC3}}
	aac := media.FormatSupport{Transport: media.TransportMPEGTS, Video: h264, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}
	tall := media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHeight: 2160, AudioCodec: media.CodecAAC, AudioChannels: 6, AudioBitrate: 320_000}
	for _, formats := range [][]media.FormatSupport{{ac3, aac}, {aac, ac3}} {
		in := Inputs{Caps: media.Capabilities{Formats: formats}, Probe: tall, Measured: true, MaxHeight: 1080, Rate: 6_500_000, Into: testFormat(t, media.MPEGTS), Encoders: hostEncoders}
		free, err := Served(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		in.MaxBitrate = 50_000_000
		capped, err := Served(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		if cast(capped) != cast(free) || !slices.Equal(capped.Refusals, free.Refusals) {
			t.Errorf("devices decoding %s then %s: under 50 Mbit/s %s %v, unbounded %s %v", formats[0].Audio.Codec, formats[1].Audio.Codec, cast(capped), capped.Refusals, cast(free), free.Refusals)
		}
	}
}

// cast is a plan as the picture's rate and the sound's codec, channels and rate.
func cast(p Plan) string {
	video, audio := p.Video.Name(), p.Audio.Name()
	if v, ok := p.Video.Encode(); ok {
		video = v.Bitrate.FFmpeg()
	}
	if a, ok := p.Audio.Encode(); ok {
		audio = fmt.Sprintf("%s/%d@%s", a.Codec, a.Channels, a.Bitrate.FFmpeg())
	}
	return video + " + " + audio
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
