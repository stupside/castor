package probe

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func probeJSON(format, streams string) []byte {
	return []byte(`{"streams":[` + streams + `],"format":{` + format + `}}`)
}

const (
	vodFormat  = `"format_name":"mov,mp4,m4a,3gp,3g2,mj2","bit_rate":"6200000","duration":"5405.400000"`
	h264Stream = `{"codec_type":"video","codec_name":"h264","profile":"High","width":1920,"height":1080,"pix_fmt":"yuv420p"}`
	aacStream  = `{"codec_type":"audio","codec_name":"aac","channels":2}`
	coverArt   = `{"codec_type":"video","codec_name":"mjpeg","width":640,"height":360,"disposition":{"attached_pic":1}}`
)

func TestDecodeSelectsThePictureAndTheDefaultAudio(t *testing.T) {
	for _, tc := range []struct {
		name       string
		streams    string
		wantVideo  media.Codec
		wantHeight int
	}{
		{"a real program is both of its tracks", h264Stream + "," + aacStream, media.CodecH264, 1080},
		{"attached cover art is not the picture", coverArt + "," + aacStream, "", 0},
		{"a thumbnail ahead of the real track is skipped", coverArt + "," + h264Stream + "," + aacStream, media.CodecH264, 1080},
		// A pull maps 0:a:0, so a later commentary track must not decide the audio axis.
		{"a later alternate audio does not replace the default", h264Stream + "," + aacStream + `,{"codec_type":"audio","codec_name":"ac3","channels":6}`, media.CodecH264, 1080},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := decode(probeJSON(vodFormat, tc.streams), 0, 0)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if info.VideoCodec != tc.wantVideo || info.VideoHeight != tc.wantHeight {
				t.Errorf("video = %q at %d, want %q at %d", info.VideoCodec, info.VideoHeight, tc.wantVideo, tc.wantHeight)
			}
			if info.AudioCodec != media.CodecAAC || info.AudioChannels != 2 {
				t.Errorf("audio = %q/%d, want the default AAC stereo track", info.AudioCodec, info.AudioChannels)
			}
		})
	}
}

func TestDecodeReadsTheContainersOwnFacts(t *testing.T) {
	for _, tc := range []struct {
		name         string
		format       string
		wantType     string
		wantBitRate  int64
		wantDuration time.Duration
	}{
		{"a VOD file states all three", vodFormat, media.MP4, 6_200_000, 5405400 * time.Millisecond},
		{"a playlist with no numbers is still a measurement", `"format_name":"hls,applehttp","bit_rate":"N/A","duration":"N/A"`, media.HLS, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := decode(probeJSON(tc.format, h264Stream+","+aacStream), 0, 0)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if info.ContentType != tc.wantType || info.BitRate != tc.wantBitRate || info.Duration != tc.wantDuration {
				t.Errorf("facts = %q/%d/%s, want %q/%d/%s", info.ContentType, info.BitRate, info.Duration, tc.wantType, tc.wantBitRate, tc.wantDuration)
			}
		})
	}
}

// Every 0:V:N slot is reported, since DASH representation choice indexes into it.
func TestDecodeReportsEveryPictureItSaw(t *testing.T) {
	streams := coverArt + `,{"codec_type":"video","codec_name":"h264","width":1280,"height":720},` +
		`{"codec_type":"video","codec_name":"h264","width":3840,"height":2160},` +
		`{"codec_type":"video","codec_name":"h264","width":0,"height":0},` + aacStream
	info, err := decode(probeJSON(vodFormat, streams), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.VideoHeights, []int{720, 2160, 0}) {
		t.Errorf("VideoHeights = %v, want every slot with the dimensionless one preserved", info.VideoHeights)
	}
	if info.VideoHeight != 2160 {
		t.Errorf("VideoHeight = %d, want the selected slot's 2160", info.VideoHeight)
	}
}

// An HLS master is read as one program per variant, which is how a rung without RESOLUTION gets a height.
func TestDecodeReportsEachProgramsPicture(t *testing.T) {
	out := []byte(`{"programs":[` +
		`{"program_id":0,"streams":[` + aacStream + `,` + coverArt + `,{"codec_type":"video","height":240}]},` +
		`{"program_id":1,"streams":[` + aacStream + `,{"codec_type":"video","height":360}]},` +
		`{"program_id":2,"streams":[` + aacStream + `]}],` +
		`"streams":[` + h264Stream + `],"format":{"format_name":"hls"}}`)
	info, err := decode(out, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int]int{0: 240, 1: 360, 2: 0}; !maps.Equal(info.ProgramHeights, want) {
		t.Errorf("ProgramHeights = %v, want %v", info.ProgramHeights, want)
	}
}

func TestTheProbeReadsWhatDecidesWhetherAPictureCanBeCopied(t *testing.T) {
	out := []byte(`{
		"streams": [
			{"index": 1, "codec_type": "audio", "codec_name": "aac", "channels": 2, "sample_rate": "96000"},
			{"index": 0, "codec_type": "video", "codec_name": "h264", "profile": "High", "level": 51, "width": 1920, "height": 1080,
			 "pix_fmt": "yuv420p", "field_order": "unknown", "side_data_list": [{"rotation": -90}]}
		],
		"frames": [{"stream_index": 1, "interlaced_frame": 0}, {"stream_index": 0, "interlaced_frame": 1}],
		"format": {"format_name": "hls", "duration": "12.0"}
	}`)
	info, err := decode(out, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !info.VideoInterlaced {
		t.Error("a picture decoded as fields is interlaced even when the demuxer reports its field order unknown")
	}
	if info.VideoLevel != 51 || info.VideoRotation != -90 || info.AudioSampleRate != 96000 {
		t.Errorf("level %d, rotation %d, sample rate %d; want 51, -90, 96000", info.VideoLevel, info.VideoRotation, info.AudioSampleRate)
	}
}

func TestAHighBitDepthPictureCannotBeCopiedAsEightBit(t *testing.T) {
	device := media.Capabilities{Video: []media.VideoSupport{{Codec: media.CodecH264}}}
	for _, format := range []string{"yuv444p9le", "yuv444p14le", "yuv444p16le"} {
		out := probeJSON(vodFormat, `{"codec_type":"video","codec_name":"h264","width":64,"height":64,"pix_fmt":"`+format+`"}`)
		info, err := decode(out, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		if device.CanCopyVideo(info) {
			t.Errorf("%s was accepted by an eight-bit device: %+v", format, info)
		}
	}
	out := probeJSON(vodFormat, `{"codec_type":"video","codec_name":"h264","width":64,"height":64,"pix_fmt":"nv12"}`)
	info, err := decode(out, 0, -1)
	if err != nil || !device.CanCopyVideo(info) {
		t.Errorf("eight-bit NV12 was refused: %+v (%v)", info, err)
	}
}
