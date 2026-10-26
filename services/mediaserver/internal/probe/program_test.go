package probe

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/dash"
	"github.com/stupside/castor/services/mediaserver/internal/source/hls"
)

// sourceProbe binds program to the probe, each input opened with its headers and the terms given for it.
func sourceProbe(t *testing.T, ffprobePath string, program media.Program, terms map[media.InputID][]string) media.Prober {
	t.Helper()
	program, err := media.NewProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]Input, 0, len(program.Inputs))
	for _, in := range program.Inputs {
		args := append(slices.Clone(terms[in.ID]), ffmpeg.HeaderArgs(in.Headers)...)
		args = append(args, source.Formats{hls.Format{}, dash.Format{}}.InputArgs(in.ContentType, 0)...)
		inputs = append(inputs, Input{ID: in.ID, URL: in.URL.String(), Args: args})
	}
	return FFprobe(ffprobePath).Source(program, inputs)
}

// fakeFFprobe logs its argv to a file and answers per the shell case body.
func fakeFFprobe(t *testing.T, cases string) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	path, log = filepath.Join(dir, "ffprobe"), filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\ncase \"$*\" in\n" + cases + "esac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

func invocations(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// A signed one-use link must be opened once, whatever tracks are selected from it.
func TestSourceProbeOpensAMuxedSelectionOnce(t *testing.T) {
	ffprobe, log := fakeFFprobe(t, `  *) printf '%s\n' '{"streams":[{"codec_type":"video","codec_name":"h264","width":64,"height":64},{"codec_type":"audio","codec_name":"aac","channels":2},{"codec_type":"audio","codec_name":"aac","channels":1}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}' ;;
`)
	program := media.Program{
		Inputs: []media.Input{{ID: "muxed", URL: mustURL(t, "https://signed.test/one-use.mp4"), ContentType: media.MP4}},
		Tracks: []media.TrackRef{
			{Input: "muxed", Kind: media.TrackVideo},
			{Input: "muxed", Kind: media.TrackAudio, Index: 1},
		},
		ClockInput: "muxed",
		EndPolicy:  media.EndAtLongest,
	}
	info, _, err := sourceProbe(t, ffprobe, program, nil).Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.VideoCodec != media.CodecH264 || info.AudioCodec != media.CodecAAC || info.AudioChannels != 1 {
		t.Errorf("probe = %+v, want video and the selected mono audio from one open", info)
	}
	if got := invocations(t, log); len(got) != 1 {
		t.Errorf("ffprobe ran %d times, want one open", len(got))
	}
}

// Each input is opened on its own terms; the clock input states the program facts.
func TestSourceProbeOpensEachInputAsTheReaderWill(t *testing.T) {
	ffprobe, log := fakeFFprobe(t, `  *'https://audio.test/sound.m4a'*) printf '%s\n' '{"streams":[{"codec_type":"audio","codec_name":"aac","channels":2},{"codec_type":"audio","codec_name":"ac3","channels":6}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","bit_rate":"111","duration":"11"}}' ;;
  *) printf '%s\n' '{"streams":[{"codec_type":"video","codec_name":"h264","width":1280,"height":720}],"format":{"format_name":"hls","bit_rate":"222","duration":"22"}}' ;;
`)
	program := media.Program{Inputs: []media.Input{
		{ID: "audio", URL: mustURL(t, "https://audio.test/sound.m4a"), Headers: http.Header{"X-Audio": {"a"}}, ContentType: media.MP4},
		{ID: "video", URL: mustURL(t, "https://video.test/live.m3u8"), Headers: http.Header{"X-Video": {"v"}}, ContentType: media.HLS},
	}, Tracks: []media.TrackRef{
		{Input: "video", Kind: media.TrackVideo},
		{Input: "audio", Kind: media.TrackAudio, Index: 1},
	}, ClockInput: "video", EndPolicy: media.EndAtLongest}
	info, _, err := sourceProbe(t, ffprobe, program, map[media.InputID][]string{
		"video": {"-rw_timeout", "1000000"},
		"audio": {"-rw_timeout", "2000000"},
	}).Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.VideoCodec != media.CodecH264 || info.VideoHeight != 720 || info.AudioCodec != media.CodecAC3 || info.AudioChannels != 6 {
		t.Errorf("merged probe = %+v, want video from the video input and the selected audio from the audio input", info)
	}
	// A playlist's bit_rate is its own size over the runtime, so it states no rate.
	if info.ContentType != media.HLS || info.BitRate != 0 || info.Duration != 22*time.Second {
		t.Errorf("program facts = %s/%d/%s, want the clock input's HLS/0/22s", info.ContentType, info.BitRate, info.Duration)
	}
	argv := strings.Join(invocations(t, log), "\n")
	for _, want := range []string{
		"-rw_timeout 1000000", "X-Video: v", "https://video.test/live.m3u8",
		"-rw_timeout 2000000", "X-Audio: a", "https://audio.test/sound.m4a",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("probe invocations lack %q:\n%s", want, argv)
		}
	}
}
