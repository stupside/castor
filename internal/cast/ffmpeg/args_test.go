package ffmpeg

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/internal/media"
)

// argValue returns the token immediately after the last occurrence of flag, or
// "" if the flag is absent. ffmpeg options are flag/value pairs, so this reads
// the value the encoder will actually receive.
func argValue(args []string, flag string) string {
	for i := len(args) - 2; i >= 0; i-- {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func TestEncodeArgsCopyRemux(t *testing.T) {
	// A nil VideoEncoder stream-copies the video.
	args, err := EncodeArgs(EncodeOptions{
		PipeFormat:      "mpegts",
		OutputFormat:    "mpegts",
		AudioCodec:      "aac",
		AudioBitrate:    "256k",
		AudioSampleRate: 48000,
		AudioChannels:   2,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := argValue(args, "-c:v"); got != "copy" {
		t.Fatalf("video codec = %q, want copy", got)
	}
	if hasFlag(args, "-preset") {
		t.Error("copy must not set -preset")
	}
	if hasFlag(args, "-b:v") {
		t.Error("copy must not set -b:v")
	}
	if hasFlag(args, "-vf") {
		t.Error("copy must not scale (no -vf)")
	}
	if hasFlag(args, "-init_hw_device") {
		t.Error("copy must not initialise a hardware device")
	}
	// A copied bitstream can't be re-rate-controlled, re-formatted, or
	// re-keyframed: those flags target the encoder, which isn't running.
	for _, f := range []string{"-maxrate", "-bufsize", "-pix_fmt", "-force_key_frames"} {
		if hasFlag(args, f) {
			t.Errorf("copy must not set %s", f)
		}
	}
	if got := argValue(args, "-c:a"); got != "aac" {
		t.Errorf("audio codec = %q, want aac", got)
	}
}

func TestEncodeArgsReadrateHeadroom(t *testing.T) {
	// Burning subtitles paces the encode with -readrate. It must be just above
	// realtime (EncodeReadrate), not 1.0: at dead-even playback speed the
	// renderer's buffer has no headroom to rebuild after jitter and rebuffers.
	args, err := EncodeArgs(EncodeOptions{
		PipeFormat:       "mpegts",
		OutputFormat:     "mpegts",
		VideoEncoder:     &libx264,
		SubtitleTextFile: "/tmp/cue.txt",
		AudioCodec:       "aac",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := argValue(args, "-readrate"); got != EncodeReadrate {
		t.Errorf("readrate = %q, want %q (headroom above realtime)", got, EncodeReadrate)
	}
}

func TestEncodeArgsLibx264(t *testing.T) {
	args, err := EncodeArgs(EncodeOptions{
		PipeFormat:          "mpegts",
		OutputFormat:        "mpegts",
		VideoEncoder:        &libx264,
		VideoBitrate:        "4M",
		VideoMaxrate:        "4M",
		VideoBufsize:        "8M",
		VideoMaxHeight:      1080,
		KeyframeIntervalSec: 2,
		AudioCodec:          "aac",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := argValue(args, "-c:v"); got != "libx264" {
		t.Fatalf("video codec = %q, want libx264", got)
	}
	if got := argValue(args, "-preset"); got != "veryfast" {
		t.Errorf("preset = %q, want veryfast", got)
	}
	if got := argValue(args, "-b:v"); got != "4M" {
		t.Errorf("bitrate = %q, want 4M", got)
	}
	if got := argValue(args, "-maxrate"); got != "4M" {
		t.Errorf("maxrate = %q, want 4M (VBV cap)", got)
	}
	if got := argValue(args, "-bufsize"); got != "8M" {
		t.Errorf("bufsize = %q, want 8M (VBV cap)", got)
	}
	if got := argValue(args, "-pix_fmt"); got != "yuv420p" {
		t.Errorf("pix_fmt = %q, want yuv420p (8-bit, Samsung-decodable)", got)
	}
	if got := argValue(args, "-force_key_frames"); got != "expr:gte(t,n_forced*2)" {
		t.Errorf("force_key_frames = %q, want the 2s GOP expression", got)
	}
	if vf := argValue(args, "-vf"); !strings.Contains(vf, "scale=-2:'min(1080,ih)'") {
		t.Errorf("-vf = %q, want software scale", vf)
	}
	if hasFlag(args, "-init_hw_device") {
		t.Error("libx264 must not initialise a hardware device")
	}
}

func TestEncodeArgsVAAPI(t *testing.T) {
	args, err := EncodeArgs(EncodeOptions{
		PipeFormat:          "mpegts",
		OutputFormat:        "mpegts",
		VideoEncoder:        &h264VAAPI,
		VideoBitrate:        "4M",
		VideoMaxrate:        "4M",
		VideoBufsize:        "8M",
		VideoMaxHeight:      1080,
		KeyframeIntervalSec: 2,
		AudioCodec:          "aac",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := argValue(args, "-init_hw_device"); got != "vaapi=va:"+vaapiRenderNode {
		t.Errorf("hw device = %q, want vaapi=va:%s", got, vaapiRenderNode)
	}
	if got := argValue(args, "-filter_hw_device"); got != "va" {
		t.Errorf("filter hw device = %q, want va", got)
	}
	if got := argValue(args, "-c:v"); got != "h264_vaapi" {
		t.Fatalf("video codec = %q, want h264_vaapi", got)
	}
	if got := argValue(args, "-b:v"); got != "4M" {
		t.Errorf("bitrate = %q, want 4M", got)
	}
	if got := argValue(args, "-maxrate"); got != "4M" {
		t.Errorf("maxrate = %q, want 4M (VBV cap applies to VA-API too)", got)
	}
	if got := argValue(args, "-bufsize"); got != "8M" {
		t.Errorf("bufsize = %q, want 8M", got)
	}
	if got := argValue(args, "-force_key_frames"); got != "expr:gte(t,n_forced*2)" {
		t.Errorf("force_key_frames = %q, want the 2s GOP expression", got)
	}
	vf := argValue(args, "-vf")
	if !strings.HasSuffix(vf, "format=nv12,hwupload") {
		t.Errorf("-vf = %q, want to end with the GPU upload (format=nv12,hwupload)", vf)
	}
	if !strings.Contains(vf, "scale=-2:'min(1080,ih)'") {
		t.Errorf("-vf = %q, want CPU scale before hwupload", vf)
	}
	// libx264 presets are invalid for the VA-API encoder; none must be emitted.
	if hasFlag(args, "-preset") {
		t.Error("h264_vaapi must not set -preset")
	}
	// VA-API downconverts via the format=nv12 filter; -pix_fmt would target the
	// GPU surface and must not appear. Nor VideoToolbox's -g workaround.
	if hasFlag(args, "-pix_fmt") {
		t.Error("h264_vaapi must not set -pix_fmt (handled by format=nv12)")
	}
	if hasFlag(args, "-g") {
		t.Error("h264_vaapi must not set -g")
	}
}

func TestEncodeArgsVideoToolbox(t *testing.T) {
	args, err := EncodeArgs(EncodeOptions{
		PipeFormat:          "mpegts",
		OutputFormat:        "mpegts",
		VideoEncoder:        &h264VideoToolbox,
		VideoBitrate:        "4M",
		VideoMaxrate:        "4M",
		VideoBufsize:        "8M",
		VideoMaxHeight:      1080,
		KeyframeIntervalSec: 2,
		AudioCodec:          "aac",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := argValue(args, "-c:v"); got != "h264_videotoolbox" {
		t.Fatalf("video codec = %q, want h264_videotoolbox", got)
	}
	if got := argValue(args, "-b:v"); got != "4M" {
		t.Errorf("bitrate = %q, want 4M", got)
	}
	if got := argValue(args, "-maxrate"); got != "4M" {
		t.Errorf("maxrate = %q, want 4M (VBV cap)", got)
	}
	if got := argValue(args, "-bufsize"); got != "8M" {
		t.Errorf("bufsize = %q, want 8M", got)
	}
	if got := argValue(args, "-pix_fmt"); got != "yuv420p" {
		t.Errorf("pix_fmt = %q, want yuv420p", got)
	}
	// VideoToolbox's default GOP is sub-second; -g 600 lifts it so
	// force_key_frames sets the real cadence.
	if got := argValue(args, "-g"); got != "600" {
		t.Errorf("-g = %q, want 600 (lift VideoToolbox's default GOP)", got)
	}
	if got := argValue(args, "-force_key_frames"); got != "expr:gte(t,n_forced*2)" {
		t.Errorf("force_key_frames = %q, want the 2s GOP expression", got)
	}
	if hasFlag(args, "-init_hw_device") {
		t.Error("videotoolbox takes system-memory frames, no hardware device init")
	}
	if vf := argValue(args, "-vf"); strings.Contains(vf, "hwupload") {
		t.Errorf("videotoolbox must not hwupload; -vf = %q", vf)
	}
}

func TestEncodeArgsSubtitlesBurnIn(t *testing.T) {
	// The planner supplies a real encoder whenever subtitles are burned in
	// (drawtext operates on decoded frames), so drawtext joins the filter chain
	// and the encoder runs.
	args, err := EncodeArgs(EncodeOptions{
		PipeFormat:       "mpegts",
		OutputFormat:     "mpegts",
		VideoEncoder:     &libx264,
		SubtitleTextFile: "/tmp/cue.txt",
		AudioCodec:       "aac",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := argValue(args, "-c:v"); got != "libx264" {
		t.Fatalf("video codec = %q, want libx264", got)
	}
	if vf := argValue(args, "-vf"); !strings.Contains(vf, "drawtext") {
		t.Errorf("-vf = %q, want drawtext burn-in", vf)
	}
}

// TestEncodeArgsSubtitlesRequireEncoder pins the guard that replaces caller
// discipline: a copied bitstream can't carry drawtext (it needs decoded
// frames), so this combination must fail loudly here instead of surfacing
// later as ffmpeg's unrelated "Filtering and streamcopy cannot be used
// together" error.
func TestEncodeArgsSubtitlesRequireEncoder(t *testing.T) {
	_, err := EncodeArgs(EncodeOptions{
		PipeFormat:       "mpegts",
		OutputFormat:     "mpegts",
		SubtitleTextFile: "/tmp/cue.txt",
		AudioCodec:       "aac",
	})
	if err == nil {
		t.Fatal("want an error for SubtitleTextFile set with a nil VideoEncoder, got nil")
	}
}

func TestSelectEncoderFallsBackToSoftware(t *testing.T) {
	// A bogus ffmpeg path makes every hardware test-encode fail, so selection
	// returns the always-available software baseline for the codec. Each codec
	// resolves to its own baseline, which is the whole point of the registry.
	for _, tc := range []struct {
		codec media.Codec
		want  string
	}{
		{media.CodecH264, "libx264"},
		{media.CodecHEVC, "libx265"},
	} {
		enc, ok := SelectEncoder(context.Background(), "/nonexistent-ffmpeg-binary", tc.codec)
		if !ok || enc.Name != tc.want {
			t.Errorf("SelectEncoder(%s) = %q (ok=%v), want %q", tc.codec, enc.Name, ok, tc.want)
		}
	}
}

func TestSelectEncoderUnknownCodec(t *testing.T) {
	// A codec with no registered encoder reports ok=false rather than guessing.
	if _, ok := SelectEncoder(context.Background(), "/nonexistent-ffmpeg-binary", media.Codec("av1")); ok {
		t.Error("SelectEncoder(av1) reported ok, but no AV1 encoder is registered")
	}
}

func TestEncodeArgsHLSOutput(t *testing.T) {
	src, err := url.Parse("http://example.test/in.mkv")
	if err != nil {
		t.Fatal(err)
	}
	args, err := EncodeArgs(EncodeOptions{
		Source:       NetworkSource{URL: src, ContentType: media.MKV},
		OutputFormat: "hls",
		AudioCodec:   "aac",
		AudioBitrate: "256k",
	})
	if err != nil {
		t.Fatal(err)
	}

	// HLS is directory output: no pipe:1, and the last token is the playlist.
	if hasFlag(args, "pipe:1") {
		t.Error("hls output must not target pipe:1")
	}
	if got := args[len(args)-1]; got != media.HLSPlaylistName {
		t.Errorf("last arg = %q, want playlist %q", got, media.HLSPlaylistName)
	}
	if got := argValue(args, "-f"); got != "hls" {
		t.Errorf("-f = %q, want hls", got)
	}
	if got := argValue(args, "-hls_segment_type"); got != "fmp4" {
		t.Errorf("-hls_segment_type = %q, want fmp4", got)
	}
	if got := argValue(args, "-hls_fmp4_init_filename"); got != media.HLSInitName {
		t.Errorf("-hls_fmp4_init_filename = %q, want %q", got, media.HLSInitName)
	}
	if got := argValue(args, "-hls_segment_filename"); got != media.HLSSegmentPattern {
		t.Errorf("-hls_segment_filename = %q, want %q", got, media.HLSSegmentPattern)
	}
	if flags := argValue(args, "-hls_flags"); !strings.Contains(flags, "delete_segments") {
		t.Errorf("-hls_flags = %q, want delete_segments for a rolling window", flags)
	}
	// playlist_type must stay unset: event/vod would pin hls_list_size to 0.
	if hasFlag(args, "-hls_playlist_type") {
		t.Error("-hls_playlist_type must be unset for a live sliding window")
	}
	// The network HLS remux MUST be paced to ~realtime: unpaced, a VOD source is
	// copied at wire speed and outruns the delete_segments window, leaving only
	// the tail fetchable. This guards that fix against regression.
	if got := argValue(args, "-readrate"); got != "1.0" {
		t.Errorf("-readrate = %q, want 1.0 so a VOD source does not outrun the sliding window", got)
	}
	if !hasFlag(args, "-readrate_initial_burst") {
		t.Error("-readrate_initial_burst must be set so the device can prebuffer one window")
	}
}

// TestEncodeArgsMP4RemuxUnpaced pins the asymmetry: a single-file network source
// is one long GET, and its mp4 remux is fronted by the replay-from-zero server
// (spooled), so it must NOT be paced: it should complete as fast as the link
// allows.
func TestEncodeArgsMP4RemuxUnpaced(t *testing.T) {
	src, err := url.Parse("http://example.test/in.mkv")
	if err != nil {
		t.Fatal(err)
	}
	args, err := EncodeArgs(EncodeOptions{
		Source:       NetworkSource{URL: src, ContentType: media.MKV},
		OutputFormat: "mp4",
		AudioCodec:   "aac",
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasFlag(args, "-readrate") {
		t.Error("the mp4 remux of a single-file source is replay-spooled from byte 0 and must not be paced")
	}
}

// TestDemuxedSourceIsTwoInputs covers a program whose renditions live at
// separate URLs: both must be opened on the same terms, and the audio map must
// follow the audio to the second input. Mapping 0:a:0 there is the failure this
// guards, since the video rendition has no audio track and ffmpeg exits with
// "Stream map ” matches no streams" before a byte is served.
func TestDemuxedSourceIsTwoInputs(t *testing.T) {
	video, err := url.Parse("http://example.test/video.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := url.Parse("http://example.test/audio.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	source := NetworkSource{
		URL:         video,
		AudioURL:    audio,
		Headers:     http.Header{"Referer": {"https://player.example/"}},
		ContentType: media.HLS,
		RWTimeout:   30 * time.Second,
	}

	for name, args := range map[string][]string{
		"pull":  PullArgs(PullOptions{Source: source}),
		"remux": mustEncodeArgs(t, EncodeOptions{Source: source, OutputFormat: "mp4", AudioCodec: "aac"}),
	} {
		inputs := inputURLs(args)
		if want := []string{video.String(), audio.String()}; !slices.Equal(inputs, want) {
			t.Errorf("%s inputs = %v, want %v", name, inputs, want)
		}
		if got := argValue(args, "-map"); got != "1:a:0" {
			t.Errorf("%s audio map = %q, want 1:a:0 (the audio is the second input)", name, got)
		}
		// Both inputs are the same origin: whatever one needs to be opened, the
		// other needs too.
		if got := countFlag(args, "-headers"); got != 2 {
			t.Errorf("%s sent headers with %d of 2 inputs", name, got)
		}
		if got := countFlag(args, "-allowed_extensions"); got != 2 {
			t.Errorf("%s relaxed extension checks on %d of 2 inputs", name, got)
		}
		if got := countFlag(args, "-readrate"); got != 2 {
			t.Errorf("%s paced %d of 2 inputs; unpaced renditions drift apart", name, got)
		}
	}
}

// TestMuxedSourceStaysOneInput is the other half: an ordinary source opens once
// and keeps mapping its own audio track.
func TestMuxedSourceStaysOneInput(t *testing.T) {
	src, err := url.Parse("http://example.test/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	args := PullArgs(PullOptions{Source: NetworkSource{URL: src, ContentType: media.HLS}})

	if got := len(inputURLs(args)); got != 1 {
		t.Errorf("inputs = %d, want 1", got)
	}
	if got := argValue(args, "-map"); got != "0:a:0" {
		t.Errorf("audio map = %q, want 0:a:0", got)
	}
}

// inputURLs returns the value of every -i in order.
func inputURLs(args []string) []string {
	var inputs []string
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			inputs = append(inputs, args[i+1])
		}
	}
	return inputs
}

func countFlag(args []string, flag string) int {
	n := 0
	for _, a := range args {
		if a == flag {
			n++
		}
	}
	return n
}

func mustEncodeArgs(t *testing.T, opts EncodeOptions) []string {
	t.Helper()
	args, err := EncodeArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	return args
}

// TestNetworkReadersShareInputPolicy pins the invariant behind NetworkSource:
// castor's two network readers open the same upstream with the same reconnect
// policy, headers, container flags, and pacing.
func TestNetworkReadersShareInputPolicy(t *testing.T) {
	src, err := url.Parse("http://example.test/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	source := NetworkSource{
		URL:         src,
		Headers:     http.Header{"Referer": {"https://player.example/"}},
		ContentType: media.HLS,
		RWTimeout:   30 * time.Second,
	}

	remux, err := EncodeArgs(EncodeOptions{Source: source, OutputFormat: "mp4", AudioCodec: "aac"})
	if err != nil {
		t.Fatal(err)
	}
	pull := PullArgs(PullOptions{Source: source})

	for _, flag := range []string{
		"-rw_timeout", "-reconnect", "-reconnect_streamed",
		"-reconnect_delay_max", "-reconnect_on_http_error",
		"-headers", "-allowed_extensions", "-extension_picky",
		"-readrate", "-readrate_initial_burst",
	} {
		if got, want := argValue(remux, flag), argValue(pull, flag); got != want {
			t.Errorf("%s: remux = %q, pull = %q; both read the same upstream and must agree", flag, got, want)
		}
	}
}

// TestEncodeArgsHLSSourcePaced pins the other half of that rule: an HLS source is
// fetched segment by segment from a rate-limiting CDN, so even the replay-spooled
// mp4 remux paces its upstream read exactly like the puller does, rather than
// pulling a whole movie as a segment burst and earning a 429.
func TestEncodeArgsHLSSourcePaced(t *testing.T) {
	src, err := url.Parse("http://example.test/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	base := EncodeOptions{
		Source:       NetworkSource{URL: src, ContentType: media.HLS},
		OutputFormat: "mp4",
		AudioCodec:   "aac",
	}

	args, err := EncodeArgs(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := argValue(args, "-readrate"); got != pacingVOD.readrate {
		t.Errorf("VOD readrate = %q, want %q (ahead of 1x playback, but not a wire-speed storm)", got, pacingVOD.readrate)
	}
	if got := argValue(args, "-readrate_initial_burst"); got != pacingVOD.burst {
		t.Errorf("VOD burst = %q, want %q", got, pacingVOD.burst)
	}

	base.Source.Live = true
	args, err = EncodeArgs(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := argValue(args, "-readrate"); got != pacingLive.readrate {
		t.Errorf("live readrate = %q, want %q (a live source cannot be outrun)", got, pacingLive.readrate)
	}
	if got := argValue(args, "-readrate_initial_burst"); got != pacingLive.burst {
		t.Errorf("live burst = %q, want %q", got, pacingLive.burst)
	}
}
