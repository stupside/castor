package encoding

import (
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var (
	mpegtsFormat = testFormat(media.MPEGTS)
	mp4Format    = testFormat(media.MP4)
	aacAudio     = codec.Encode(codec.AudioEncode{Codec: media.CodecAAC})
)

func testFormat(contentType string) container.Format {
	f, ok := container.For(contentType)
	if !ok {
		panic("no producible format for " + contentType)
	}
	return f
}

func TestOmittedTracksAreNotMappedOrAdapted(t *testing.T) {
	source := muxedSource(t, mustURL(t, "http://example.test/movie.mkv"), media.MKV, fetch.Policy{})
	for _, input := range []EncodeInput{FromPipe(fetch.Pace{}), FromSource(source)} {
		for _, omitVideo := range []bool{true, false} {
			opts := EncodeOptions{Input: input, Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: codec.CopyAudio()}
			omitted, disable := "0:V:0?", "-vn"
			if omitVideo {
				// VP9 is not carried in this container, but this plan omits it.
				opts.Video = codec.OmitVideo()
				opts.Probe = media.ProbeInfo{VideoCodec: media.CodecVP9, AudioCodec: media.CodecAAC}
			} else {
				omitted, disable = "0:a:0?", "-an"
				opts.Audio = codec.OmitAudio()
				opts.Probe = media.ProbeInfo{VideoCodec: media.CodecH264, AudioCodec: media.CodecFLAC}
			}
			args := mustEncodeArgs(t, opts)
			if slices.Contains(args, omitted) || !slices.Contains(args, disable) {
				t.Fatalf("omitted track was mapped: %v", args)
			}
			if opts.Verbatim() {
				t.Fatal("dropping a known track reused the whole spool")
			}
		}
	}
}

func TestUnfinishedOrEmptyTrackPlanCannotBuildACommand(t *testing.T) {
	for _, opts := range []EncodeOptions{
		{Video: codec.CopyVideo()},
		{Audio: codec.CopyAudio()},
		{Video: codec.OmitVideo(), Audio: codec.OmitAudio()},
	} {
		opts.Input, opts.Format = FromPipe(fetch.Pace{}), mpegtsFormat
		if _, err := EncodeArgs(opts); err == nil {
			t.Fatalf("unfinished or empty track plan was rendered: %+v", opts)
		}
		if opts.Verbatim() {
			t.Fatal("unfinished or empty track plan reused a spool")
		}
	}
}

func TestOmittingAnUnidentifiedTrackCannotReuseTheWholeSpool(t *testing.T) {
	for _, opts := range []EncodeOptions{
		{Video: codec.OmitVideo(), Audio: codec.CopyAudio(), Probe: media.ProbeInfo{VideoPresent: true, AudioCodec: media.CodecAAC}},
		{Video: codec.CopyVideo(), Audio: codec.OmitAudio(), Probe: media.ProbeInfo{VideoCodec: media.CodecH264, AudioPresent: true}},
	} {
		opts.Input, opts.Format = FromPipe(fetch.Pace{}), mpegtsFormat
		if opts.Verbatim() {
			t.Fatal("omitting an unidentified stream reused the whole spool")
		}
	}
}

func TestTheArgvRendersTheDecision(t *testing.T) {
	src := muxedSource(t, mustURL(t, "http://example.test/in.m3u8"), media.HLS, fetch.Policy{})
	piped := FromPipe(fetch.Pace{})
	scale := "scale=-2:'min(1080,max(2,trunc(ih/2)*2))'"
	hardware := ffmpeg.Encoder{
		Name: "h264_test_hw", Codec: media.CodecH264, Hardware: true,
		InitArgs: []string{"-init_hw_device", "test=host"},
		Filters:  []string{"format=nv12", "hwupload"},
		Flags:    []string{"-preset", "fast"},
	}
	copyInto := func(format container.Format, probe media.ProbeInfo) []string {
		return mustEncodeArgs(t, EncodeOptions{Input: piped, Format: format, Probe: probe, Video: codec.CopyVideo(), Audio: codec.CopyAudio()})
	}
	baseMov := "+frag_keyframe+empty_moov+default_base_moof"

	for _, tt := range []struct {
		name   string
		args   []string
		want   []string
		absent []string
		vf     string
	}{{
		name:   "a copying pull passes both axes through",
		args:   mustPullArgs(t, copyingPull(src)),
		want:   []string{"-c:v", "copy", "-c:a", "copy", "-f", SpoolFormat.Muxer, ffmpeg.StdoutPipe},
		absent: []string{"-vf", "-bsf:a", "-copyts"},
	}, {
		name: "a pull that may move to another rung keeps the source's clock",
		args: mustPullArgs(t, PullOptions{Source: src, Audio: codec.CopyAudio(), Video: codec.CopyVideo(), KeepTimeline: true}),
		want: []string{"-copyts"},
	}, {
		name: "a software floor pull scales to the ceiling under a quality target",
		args: mustPullArgs(t, PullOptions{Source: src, Audio: codec.CopyAudio(), Video: codec.Encode(codec.VideoEncode{
			Encoder: libx264, Quality: 23, Maxrate: 4_000_000, Bufsize: 8_000_000, MaxHeight: 1080,
		})}),
		want: []string{
			"-vf", scale, "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
			"-crf", "23", "-maxrate", "4000k", "-bufsize", "8000k", "-c:a", "copy",
		},
	}, {
		name: "a hardware encode uploads after the scale and runs at a bitrate",
		args: mustEncodeArgs(t, EncodeOptions{Input: piped, Format: mpegtsFormat, Audio: aacAudio, Video: codec.Encode(codec.VideoEncode{
			Encoder: hardware, Bitrate: 4_000_000, MaxHeight: 1080, KeyframeIntervalSec: 2,
		})}),
		want: []string{
			"-vf", scale + ",format=nv12,hwupload", "-c:v", "h264_test_hw", "-preset", "fast",
			"-b:v", "4000k", "-force_key_frames", "expr:gte(t,n_forced*2)",
		},
	}, {
		name:   "a copied picture beside an encoded sound, codecs before -strict, reporting on pipe 3",
		args:   mustEncodeArgs(t, EncodeOptions{Input: piped, Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: codec.Encode(codec.AudioEncode{Codec: media.CodecAAC, Bitrate: 256_000, Channels: 2})}),
		want:   []string{"-c:v", "copy", "-c:a", "aac", "-ac", "2", "-b:a", "256k", "-strict", "-2", "-progress", "pipe:3"},
		absent: []string{"-vf", "-preset", "-b:v", "-readrate", "-stats_period"},
	}, {
		name: "a piped encode renders its pace ahead of the pipe input",
		args: mustEncodeArgs(t, EncodeOptions{Input: FromPipe(fetch.Pace{Realtime: 1, Burst: 32 * time.Second}), Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: aacAudio}),
		want: slices.Concat([]string{"-readrate", "1.0", "-readrate_initial_burst", "32"}, demuxFlags, []string{"-f", SpoolFormat.Muxer, "-i", "pipe:0"}),
	}, {
		name:   "an encode that joins others of one buffer keeps the buffer's timestamps",
		args:   mustEncodeArgs(t, EncodeOptions{Input: piped, Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: aacAudio, KeepTimeline: true}),
		want:   []string{"-copyts"},
		absent: []string{"-output_ts_offset"},
	}, {
		name:   "an encode on its own rebases them",
		args:   mustEncodeArgs(t, EncodeOptions{Input: piped, Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: aacAudio}),
		absent: []string{"-copyts"},
	}, {
		name: "a burn-in draws text and reports every tenth of a second",
		args: mustEncodeArgs(t, EncodeOptions{Input: piped, Format: mpegtsFormat, Audio: aacAudio, Video: codec.Encode(codec.VideoEncode{Encoder: libx264, SubtitleTextFile: "/tmp/cue.txt"})}),
		want: []string{"-stats_period", "0.1"},
		vf:   "drawtext=textfile=/tmp/cue.txt",
	}, {
		name:   "ADTS AAC copied into mp4 is repacked",
		args:   copyInto(mp4Format, media.ProbeInfo{VideoCodec: media.CodecH264, AudioCodec: media.CodecAAC}),
		want:   []string{"-bsf:a", "aac_adtstoasc", "-strict", "-2", "-movflags", baseMov, "-progress"},
		absent: []string{"-frag_duration", "-tag:v"},
	}, {
		name:   "AAC copied into MPEG-TS keeps its headers",
		args:   copyInto(mpegtsFormat, media.ProbeInfo{VideoCodec: media.CodecH264, AudioCodec: media.CodecAAC}),
		absent: []string{"-bsf:a"},
	}, {
		name: "AC-3 into mp4 delays the moov and bounds the delay",
		args: copyInto(mp4Format, media.ProbeInfo{VideoCodec: media.CodecH264, AudioCodec: media.CodecAC3}),
		want: []string{"-frag_duration", "1000000", "-movflags", baseMov + "+delay_moov"},
	}, {
		name: "HEVC copied into mp4 is tagged hvc1",
		args: copyInto(mp4Format, media.ProbeInfo{VideoCodec: media.CodecHEVC, AudioCodec: media.CodecAAC}),
		want: []string{"-tag:v", "hvc1"},
	}} {
		t.Run(tt.name, func(t *testing.T) {
			if !containsSequence(tt.args, tt.want) {
				t.Errorf("argv\n%q\nwant the sequence\n%q", tt.args, tt.want)
			}
			for _, flag := range tt.absent {
				if slices.Contains(tt.args, flag) {
					t.Errorf("argv carries %s: %q", flag, tt.args)
				}
			}
			if i := slices.Index(tt.args, "-init_hw_device"); i >= 0 && i > slices.Index(tt.args, "-i") {
				t.Errorf("hardware init must precede the input: %q", tt.args)
			}
			if vf := argValue(tt.args, "-vf"); !strings.Contains(vf, tt.vf) {
				t.Errorf("-vf = %q, want it to contain %q", vf, tt.vf)
			}
		})
	}
}

func TestOnlyAnEncodeThatWouldRewriteItsSpoolUnchangedIsVerbatim(t *testing.T) {
	piped := FromPipe(fetch.Pace{})
	segmentedTS := mpegtsFormat
	segmentedTS.Delivery = container.DeliverSegmented
	src := muxedSource(t, mustURL(t, "http://example.test/in.ts"), media.MPEGTS, fetch.Policy{})
	h264 := codec.Encode(codec.VideoEncode{Encoder: libx264})

	for _, tt := range []struct {
		name string
		opts EncodeOptions
		want bool
	}{
		{"both axes copied from the spool into its own container", EncodeOptions{Input: piped, Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: codec.CopyAudio()}, true},
		{"an encoded picture", EncodeOptions{Input: piped, Format: mpegtsFormat, Video: h264, Audio: codec.CopyAudio()}, false},
		{"an encoded sound", EncodeOptions{Input: piped, Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: aacAudio}, false},
		{"another container", EncodeOptions{Input: piped, Format: mp4Format, Video: codec.CopyVideo(), Audio: codec.CopyAudio()}, false},
		{"a segmented delivery", EncodeOptions{Input: piped, Format: segmentedTS, Video: codec.CopyVideo(), Audio: codec.CopyAudio()}, false},
		{"the source rather than the spool", EncodeOptions{Input: FromSource(src), Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: codec.CopyAudio()}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.Verbatim(); got != tt.want {
				t.Errorf("Verbatim = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestIllegalCommandsAreRefused(t *testing.T) {
	piped := FromPipe(fetch.Pace{})
	for _, tt := range []struct {
		name string
		opts EncodeOptions
	}{
		{"no input", EncodeOptions{Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: aacAudio}},
		{"a copy the container refuses", EncodeOptions{Input: piped, Format: mpegtsFormat, Probe: media.ProbeInfo{VideoCodec: media.CodecH264, AudioCodec: media.CodecFLAC}, Video: codec.CopyVideo(), Audio: codec.CopyAudio()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := EncodeArgs(tt.opts); err == nil {
				t.Fatal("want a refusal, got a command line")
			}
		})
	}
}

func TestPipesAreCountedFromTheArgvAndMatchTheConsumer(t *testing.T) {
	src := muxedSource(t, mustURL(t, "http://example.test/in.mp4"), media.MP4, fetch.Policy{})
	teeing := copyingPull(src)
	teeing.PCMSampleRate = 16000
	for _, tt := range []struct {
		name  string
		opts  PullOptions
		with  ffmpeg.Options
		pipes int
	}{
		{"a tee nobody reads", teeing, ffmpeg.Options{}, 2},
		{"a consumer of a tee never routed", copyingPull(src), ffmpeg.Options{PCM: io.Discard}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := PullArgs(tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if cmd.ExtraPipes != tt.pipes {
				t.Errorf("extra pipes = %d, want %d", cmd.ExtraPipes, tt.pipes)
			}
			if _, err := ffmpeg.Start(t.Context(), "/bin/true", cmd, tt.with); err == nil {
				t.Fatal("started anyway")
			}
		})
	}
}

// libx264 is the software H.264 encoder as ffmpeg.Encoders states it.
var libx264 = ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264, Flags: []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func argValue(args []string, flag string) string {
	for i := len(args) - 2; i >= 0; i-- {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func containsSequence(args, want []string) bool {
	for i := range len(args) - len(want) + 1 {
		if slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func mustEncodeArgs(t *testing.T, opts EncodeOptions) []string {
	t.Helper()
	cmd, err := EncodeArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	return cmd.Args
}

func copyingPull(source ProgramSource) PullOptions {
	return PullOptions{Source: source, Video: codec.CopyVideo(), Audio: codec.CopyAudio()}
}

func mustPullArgs(t *testing.T, opts PullOptions) []string {
	t.Helper()
	cmd, err := PullArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	return cmd.Args
}

func TestOnlyABinaryThatHoldsALaggingReadIsToldHowToCatchUp(t *testing.T) {
	live := fetch.Pace{Realtime: 1, Catchup: 2}
	if got := strings.Join(paceArgs(live, true), " "); !strings.Contains(got, "-readrate_catchup 2.0") {
		t.Errorf("pace = %q, want the catch-up rate for a binary that needs it", got)
	}
	if got := strings.Join(paceArgs(live, false), " "); strings.Contains(got, "readrate_catchup") {
		t.Errorf("pace = %q, an older binary rejects a flag it catches up without", got)
	}
}

// A Windows temp path carries a drive colon and backslashes, and every other filtergraph metacharacter when the user name does.
func TestFilterArgEscapingSurvivesAWindowsPath(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := filepath.Join(t.TempDir(), `C:\Users\O'Brien,[x];y`)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "in.nut")
	if out, err := exec.CommandContext(t.Context(), ffmpegPath, "-hide_banner",
		"-f", "lavfi", "-i", "color=d=0.2", src).CombinedOutput(); err != nil {
		t.Fatalf("generating %q: %v\n%s", src, err, out)
	}
	// movie= reads its path through the same two parsers as the burn-in's drawtext, and is in every ffmpeg build.
	if out, err := exec.CommandContext(t.Context(), ffmpegPath, "-hide_banner",
		"-f", "lavfi", "-i", "movie=filename="+escapeFilterArg(src), "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("filter could not open %q: %v\n%s", src, err, out)
	}
}
