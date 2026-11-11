package media

import "testing"

func TestAnUnidentifiedTrackCannotAuthorizePlayback(t *testing.T) {
	caps := Capabilities{Formats: []FormatSupport{{Transport: TransportMP4,
		Video: VideoSupport{Codec: CodecH264}, Audio: AudioSupport{Codec: CodecAAC}}}}
	for _, info := range []ProbeInfo{
		{VideoPresent: true, AudioCodec: CodecAAC},
		{AudioPresent: true, VideoCodec: CodecH264, VideoBitDepth: 8},
	} {
		if caps.CanPlayIn(MP4, info) || caps.CanPlaySourceIn(MP4, info) {
			t.Fatalf("an unidentified stream was treated as absent: %+v", info)
		}
	}
}

func TestDeviceCanCopyVideo(t *testing.T) {
	envelope := Capabilities{Formats: []FormatSupport{{Transport: TransportMP4, Video: VideoSupport{Codec: CodecH264, Profiles: []Profile{"Constrained Baseline", "Baseline", "Main", "High"}}}}}
	base := ProbeInfo{VideoCodec: CodecH264, VideoProfile: "High", VideoHeight: 1080, VideoBitDepth: 8}
	for _, tt := range []struct {
		name   string
		mutate func(*ProbeInfo)
		want   bool
	}{
		{"in-envelope high", func(*ProbeInfo) {}, true},
		{"resolution is not part of the envelope", func(p *ProbeInfo) { p.VideoHeight = 2160 }, true},
		{"high 10 rejected", func(p *ProbeInfo) { p.VideoProfile = "High 10" }, false},
		{"unknown profile rejected", func(p *ProbeInfo) { p.VideoProfile = "" }, false},
		{"10-bit rejected", func(p *ProbeInfo) { p.VideoBitDepth = 10 }, false},
		{"hevc rejected", func(p *ProbeInfo) { p.VideoCodec = CodecHEVC }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := base
			tt.mutate(&info)
			if got := envelope.CanCopyVideo(info); got != tt.want {
				t.Errorf("CanCopyVideo = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeviceCanCopyAudio(t *testing.T) {
	envelope := Capabilities{Formats: []FormatSupport{{Transport: TransportMP4, Audio: AudioSupport{Codec: CodecAAC, MaxChannels: 2}}, {Transport: TransportMP4, Audio: AudioSupport{Codec: CodecAC3}}}}
	for _, tt := range []struct {
		name  string
		codec Codec
		ch    int
		want  bool
	}{
		{"stereo aac copies", CodecAAC, 2, true},
		{"5.1 aac exceeds the stereo aac ceiling", CodecAAC, 6, false},
		{"5.1 ac3 copies", CodecAC3, 6, true},
		{"unadvertised codec rejected", CodecEAC3, 6, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := envelope.CanCopyAudio(ProbeInfo{AudioCodec: tt.codec, AudioChannels: tt.ch}); got != tt.want {
				t.Errorf("CanCopyAudio = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAudioOnlyCapabilities(t *testing.T) {
	caps := Capabilities{Formats: []FormatSupport{{Transport: TransportMPEGTS, Audio: AudioSupport{Codec: CodecAAC}}}}
	if !caps.CanPlayIn(MPEGTS, ProbeInfo{AudioCodec: CodecAAC, AudioChannels: 2}) {
		t.Fatal("audio-only tuple rejected")
	}
	if caps.CanPlayIn(MPEGTS, ProbeInfo{}) {
		t.Fatal("unprobed empty media accepted")
	}
}

func TestWrappedHLSRequiresOneCompatibleEnvelope(t *testing.T) {
	device := Capabilities{Formats: []FormatSupport{
		{Transport: TransportHLS, Video: VideoSupport{Codec: CodecH264}},
		{Transport: TransportHLS, Video: VideoSupport{Codec: CodecHEVC}, ExtraSegments: []HLSExtraSegmentFormat{HLSExtraSegmentTIKTIKPX}},
	}}
	plain := ProbeInfo{VideoCodec: CodecH264, VideoBitDepth: 8}
	if !device.CanPlaySourceIn(HLS, plain) {
		t.Fatal("ordinary HLS rejected")
	}
	plain.SourceUnwrapped = true
	if device.CanPlaySourceIn(HLS, plain) {
		t.Fatal("combined incompatible HLS envelopes")
	}
	wrappedHEVC := ProbeInfo{VideoCodec: CodecHEVC, VideoBitDepth: 8, SourceUnwrapped: true}
	if !device.CanPlaySourceIn(HLS, wrappedHEVC) {
		t.Fatal("wrapped HLS rejected by a compatible envelope")
	}
}

func TestTheHalvesAReceiverRefusesAreNamed(t *testing.T) {
	ts := func(video VideoSupport, audio AudioSupport) FormatSupport {
		return FormatSupport{Transport: TransportMPEGTS, Video: video, Audio: audio}
	}
	h264, hevc := VideoSupport{Codec: CodecH264}, VideoSupport{Codec: CodecHEVC}
	aac, ac3 := AudioSupport{Codec: CodecAAC, MaxChannels: 2}, AudioSupport{Codec: CodecAC3, MaxChannels: 2}
	p := ProbeInfo{VideoCodec: CodecH264, VideoBitDepth: 8, AudioCodec: CodecAAC, AudioChannels: 2}
	for _, tt := range []struct {
		name    string
		formats []FormatSupport
		want    Axes
	}{
		{"a stream it plays", []FormatSupport{ts(h264, aac)}, Axes{}},
		{"a picture it cannot decode", []FormatSupport{ts(hevc, aac)}, Axes{Video: true}},
		{"a sound it cannot decode", []FormatSupport{ts(h264, ac3)}, Axes{Audio: true}},
		{"halves it decodes only apart", []FormatSupport{ts(h264, ac3), ts(hevc, aac)}, Axes{Video: true, Audio: true}},
	} {
		if got := (Capabilities{Formats: tt.formats}).Refused(MPEGTS, p); got != tt.want {
			t.Errorf("%s: refused %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestAnInterlacedPictureIsPlayedOnlyByADeviceThatDeinterlaces(t *testing.T) {
	interlaced := ProbeInfo{VideoCodec: CodecH264, VideoProfile: "High", VideoHeight: 1080, VideoBitDepth: 8, VideoInterlaced: true}
	for _, deinterlaces := range []bool{false, true} {
		caps := Capabilities{Deinterlaces: deinterlaces, Formats: []FormatSupport{{Transport: TransportMPEGTS, Video: VideoSupport{Codec: CodecH264}}}}
		if caps.CanCopyVideo(interlaced) != deinterlaces || caps.CanPlayIn(MPEGTS, interlaced) != deinterlaces || caps.Refused(MPEGTS, interlaced).Video == deinterlaces {
			t.Errorf("deinterlaces %t: copy %t, plays %t, refused %v", deinterlaces, caps.CanCopyVideo(interlaced), caps.CanPlayIn(MPEGTS, interlaced), caps.Refused(MPEGTS, interlaced))
		}
	}
}

func TestSoundAboveWhatDevicesPlayIsPlayedNowhere(t *testing.T) {
	hiRes := ProbeInfo{AudioCodec: CodecAAC, AudioChannels: 2, AudioSampleRate: 96000}
	caps := Capabilities{Formats: []FormatSupport{{Transport: TransportMP4, Audio: AudioSupport{Codec: CodecAAC, MaxChannels: 2}}}}
	if caps.CanCopyAudio(hiRes) || caps.CanPlayIn(MP4, hiRes) || caps.CanPlaySourceIn(MP4, hiRes) {
		t.Errorf("96 kHz sound: copy %t, plays %t, source plays %t, want none: no device castor serves plays it", caps.CanCopyAudio(hiRes), caps.CanPlayIn(MP4, hiRes), caps.CanPlaySourceIn(MP4, hiRes))
	}
}
