package encoding

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/dash"
	"github.com/stupside/castor/services/mediaserver/internal/source/hls"
)

// formats are the source formats inputs are opened with, as the media server binds them.
var formats = source.Formats{hls.Format{}, dash.Format{}}

func TestEachInputIsOpenedOnItsOwnTerms(t *testing.T) {
	program := mustProgram(t, media.Program{Inputs: []media.Input{
		{ID: "picture", URL: mustURL(t, "https://video.test/master.m3u8"), Headers: http.Header{"X-Video-Token": {"v"}}, ContentType: media.HLS},
		{ID: "sound", URL: mustURL(t, "https://audio.test/track.m4a"), Headers: http.Header{"X-Audio-Token": {"a"}}, ContentType: media.MP4},
	}, Tracks: []media.TrackRef{
		{Input: "picture", Kind: media.TrackVideo},
		{Input: "sound", Kind: media.TrackAudio, Index: 2},
	}, ClockInput: "picture", EndPolicy: media.EndAtLongest})
	source := mustProgramSource(t, program, fetch.Plan{
		"picture": {
			Deadline: time.Second, SegmentRetries: 4,
			Pace: fetch.Pace{Realtime: 2, Burst: 90 * time.Second},
		},
		// A direct file is never handed -seg_max_retry, which its demuxer aborts on.
		"sound": {Deadline: 2 * time.Second, SegmentRetries: 4},
	})
	args := mustPullArgs(t, copyingPull(source))

	for _, want := range [][]string{
		slices.Concat(demuxFlags, []string{
			"-readrate", "2.0", "-readrate_initial_burst", "90",
		}, ffmpeg.ProtocolArgs(), []string{
			"-rw_timeout", "1000000",
			"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "60", "-reconnect_on_http_error", "429,500,502,503,504",
			"-headers", "X-Video-Token: v\r\n",
			"-f", "hls", "-http_seekable", "0", "-http_persistent", "0", "-prefer_x_start", "1", "-allowed_extensions", "ALL", "-allowed_segment_extensions", "ALL",
			"-extension_picky", "0", "-seg_format_options", "extension_picky=0",
			"-seg_max_retry", "4", "-i", "https://video.test/master.m3u8",
		}),
		slices.Concat(demuxFlags, ffmpeg.ProtocolArgs(), []string{"-rw_timeout", "2000000",
			"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "60", "-reconnect_on_http_error", "429,500,502,503,504",
			"-headers", "X-Audio-Token: a\r\n", "-i", "https://audio.test/track.m4a"}),
		{"-map", "0:V:0", "-map", "1:a:2"},
	} {
		if !containsSequence(args, want) {
			t.Errorf("argv\n%q\nwant the sequence\n%q", args, want)
		}
	}
}

func TestOnlySelectedTracksAreMapped(t *testing.T) {
	program := mustProgram(t, media.Program{
		Inputs:     []media.Input{{ID: "silent", URL: mustURL(t, "https://media.test/silent.mp4"), ContentType: media.MP4}},
		Tracks:     []media.TrackRef{{Input: "silent", Kind: media.TrackVideo, Optional: true}},
		ClockInput: "silent",
		EndPolicy:  media.EndAtLongest,
	})
	args := mustPullArgs(t, copyingPull(mustProgramSource(t, program, nil)))
	var maps []string
	for i, a := range args[:len(args)-1] {
		if a == "-map" {
			maps = append(maps, args[i+1])
		}
	}
	if !slices.Equal(maps, []string{"0:V:0?"}) {
		t.Errorf("maps = %q, want only the optional video", maps)
	}
}

func TestOffsetsAndEndPolicyAreRendered(t *testing.T) {
	program := mustProgram(t, media.Program{Inputs: []media.Input{
		{ID: "video", URL: mustURL(t, "https://media.test/video.mp4"), ContentType: media.MP4},
		{ID: "audio", URL: mustURL(t, "https://media.test/audio.m4a"), ContentType: media.MP4},
	}, Tracks: []media.TrackRef{{Input: "video", Kind: media.TrackVideo}, {Input: "audio", Kind: media.TrackAudio}}, ClockInput: "video",
		Offsets:   map[media.InputID]time.Duration{"audio": 1250 * time.Millisecond},
		EndPolicy: media.EndAtShortest})
	source := mustProgramSource(t, program, nil)
	for name, args := range map[string][]string{
		"pull":   mustPullArgs(t, copyingPull(source)),
		"encode": mustEncodeArgs(t, EncodeOptions{Input: FromSource(source), Format: mp4Format, Video: codec.CopyVideo(), Audio: aacAudio}),
	} {
		if !containsSequence(args, []string{"-itsoffset", "1.25", "-i", "https://media.test/audio.m4a"}) {
			t.Errorf("%s: audio offset is not attached to its input: %q", name, args)
		}
		if !slices.Contains(args, "-shortest") {
			t.Errorf("%s: end-at-shortest did not render -shortest: %q", name, args)
		}
	}
}

// A probe opens each input on the terms its read will, so what it measures is what the read gets.
func TestAProbeOpensEachInputAsItsReadWill(t *testing.T) {
	program := mustProgram(t, media.Program{Inputs: []media.Input{
		{ID: "picture", URL: mustURL(t, "https://video.test/live.m3u8"), Headers: http.Header{"X-Video": {"v"}}, ContentType: media.HLS},
		{ID: "sound", URL: mustURL(t, "https://audio.test/sound.m4a"), Headers: http.Header{"X-Audio": {"a"}}, ContentType: media.MP4},
	}, Tracks: []media.TrackRef{
		{Input: "picture", Kind: media.TrackVideo},
		{Input: "sound", Kind: media.TrackAudio, Index: 1},
	}, ClockInput: "picture", EndPolicy: media.EndAtLongest})
	inputs := mustProgramSource(t, program, fetch.Plan{
		"picture": {Deadline: time.Second, SegmentRetries: 4},
		"sound":   {Deadline: 2 * time.Second},
	}).ProbeInputs()
	want := map[media.InputID][][]string{
		"picture": {{"-rw_timeout", "1000000"}, {"-headers", "X-Video: v\r\n"}, {"-f", "hls"}, {"-seg_max_retry", "4"}},
		"sound":   {{"-rw_timeout", "2000000"}, {"-headers", "X-Audio: a\r\n"}},
	}
	for _, in := range inputs {
		for _, terms := range want[in.ID] {
			if !containsSequence(in.Args, terms) {
				t.Errorf("the %s probe opens with %q, which lacks %q", in.ID, in.Args, terms)
			}
		}
	}
	if len(inputs) != 2 || inputs[0].URL != "https://video.test/live.m3u8" || inputs[1].URL != "https://audio.test/sound.m4a" {
		t.Errorf("probe inputs = %+v, want each input in program order", inputs)
	}
}

func mustProgram(t *testing.T, program media.Program) media.Program {
	t.Helper()
	program, err := media.NewProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func mustProgramSource(t *testing.T, program media.Program, policies fetch.Plan) ProgramSource {
	t.Helper()
	if policies == nil {
		policies = make(fetch.Plan, len(program.Inputs))
		for _, input := range program.Inputs {
			policies[input.ID] = fetch.Policy{}
		}
	}
	source, err := NewProgramSource(program, policies, ffmpeg.Binary{}, formats.InputArgs)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func muxedSource(t *testing.T, u *url.URL, contentType string, policy fetch.Policy) ProgramSource {
	t.Helper()
	program := mustProgram(t, media.Program{
		Inputs: []media.Input{{ID: media.PrimaryInputID, URL: u, ContentType: contentType}},
		Tracks: []media.TrackRef{
			{Input: media.PrimaryInputID, Kind: media.TrackVideo, Optional: true},
			{Input: media.PrimaryInputID, Kind: media.TrackAudio, Optional: true},
		},
		ClockInput: media.PrimaryInputID,
		EndPolicy:  media.EndAtLongest,
	})
	return mustProgramSource(t, program, fetch.Plan{media.PrimaryInputID: policy})
}

// ffmpeg holds the discontinuity threshold for the whole read, so a program states it once or not at all.
func TestAProgramWithASeamDeclaresItsDiscontinuityThresholdOnce(t *testing.T) {
	for _, tt := range []struct {
		name  string
		fetch []media.Fetch
		want  int
	}{
		{"no input seamed", []media.Fetch{{}, {}}, 0},
		{"one input seamed", []media.Fetch{{Spliced: true}, {}}, 1},
		{"every input seamed", []media.Fetch{{Spliced: true}, {Live: true}}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			program := mustProgram(t, media.Program{Inputs: []media.Input{
				{ID: "picture", URL: mustURL(t, "https://video.test/master.m3u8"), ContentType: media.HLS, Fetch: tt.fetch[0]},
				{ID: "sound", URL: mustURL(t, "https://audio.test/sound.m3u8"), ContentType: media.HLS, Fetch: tt.fetch[1]},
			}, Tracks: []media.TrackRef{
				{Input: "picture", Kind: media.TrackVideo},
				{Input: "sound", Kind: media.TrackAudio},
			}, ClockInput: "picture", EndPolicy: media.EndAtLongest})
			args := mustPullArgs(t, copyingPull(mustProgramSource(t, program, fetch.Plan{"picture": {}, "sound": {}})))
			at := slices.Index(args, "-dts_delta_threshold")
			if got := strings.Count(strings.Join(args, " "), "-dts_delta_threshold"); got != tt.want {
				t.Fatalf("the threshold is declared %d times, want %d: %v", got, tt.want, args)
			}
			if tt.want > 0 && at > slices.Index(args, "-i") {
				t.Errorf("the threshold follows an input it holds for: %v", args)
			}
		})
	}
}

func TestAResumedProgramSeeksEveryInputBeforeOpeningIt(t *testing.T) {
	program := mustProgram(t, media.Program{Inputs: []media.Input{
		{ID: "video", URL: mustURL(t, "https://media.test/video.mp4"), ContentType: media.MP4},
		{ID: "audio", URL: mustURL(t, "https://media.test/audio.m4a"), ContentType: media.MP4},
	}, Tracks: []media.TrackRef{{Input: "video", Kind: media.TrackVideo}, {Input: "audio", Kind: media.TrackAudio}}, ClockInput: "video",
		EndPolicy: media.EndAtShortest, Start: 816500 * time.Millisecond})
	args := mustPullArgs(t, copyingPull(mustProgramSource(t, program, nil)))
	for _, input := range []string{"video.mp4", "audio.m4a"} {
		if !containsSequence(args, []string{"-ss", "816.5", "-i", "https://media.test/" + input}) {
			t.Errorf("%s is not seeked as it opens: %q", input, args)
		}
	}
}
