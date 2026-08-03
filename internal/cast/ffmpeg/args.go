package ffmpeg

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/internal/media"
)

// CodecCopy is the ffmpeg "-c copy" keyword: stream-copy a track instead of
// re-encoding it. It is the one non-encoder value AudioCodec takes (VideoEncoder
// uses a nil pointer for the same intent), named so callers set and test it
// without repeating the bare "copy" literal.
const CodecCopy = "copy"

// NetworkSource is an upstream castor reads over the network: the URL plus
// everything a fetch of it needs to succeed and behave. Castor has exactly two
// network readers (the spool puller and the served remux) and both describe
// their upstream with this one type, so the whole input side of their command
// lines is assembled by inputArgs and cannot drift apart.
type NetworkSource struct {
	URL *url.URL

	// AudioURL is the companion audio rendition of a demuxed program, read as a
	// second input alongside URL. nil is the ordinary muxed case.
	AudioURL *url.URL

	// Headers are the HTTP request headers ffmpeg sends when fetching URL
	// (Referer, Origin, Cookie, User-Agent: what a proxied CDN gates on).
	Headers http.Header

	// ContentType is the source's container. It selects the container-specific
	// input flags (see containerInputArgs) and whether the source is fetched as
	// segments (see segmented).
	ContentType string

	// Live marks a genuinely live stream: it arrives at 1x and cannot be
	// outrun, so it is read at wall-clock speed with no burst.
	Live bool

	// RWTimeout is how long a single upstream read may stall before ffmpeg
	// gives up on it and reconnects.
	RWTimeout time.Duration
}

// NewNetworkSource describes a resolved stream as an upstream to read. Both
// readers build their source through it, so a pull and a remux of the same
// stream fetch it identically.
func NewNetworkSource(stream *media.Stream, rwTimeout time.Duration) NetworkSource {
	return NetworkSource{
		URL:         stream.URL,
		AudioURL:    stream.AudioURL,
		Headers:     stream.Headers,
		ContentType: stream.ContentType,
		Live:        stream.Live,
		RWTimeout:   rwTimeout,
	}
}

// EncodeOptions is the full description of an encode invocation. Every choice
// is explicit; nothing is inferred from globals or context. The planner
// upstream is responsible for filling these in based on device capabilities
// and source media properties.
type EncodeOptions struct {
	// PipeFormat, when non-empty, names the demuxer for stdin input
	// ("mpegts"); the caller feeds the source via WithStdin. Used to encode
	// from the local spool: pipes never report EOF until the writer closes,
	// which is what lets ffmpeg consume a still-growing stream.
	PipeFormat string

	// Source is the network input, read when PipeFormat is empty and ignored
	// otherwise.
	Source NetworkSource

	// OutputFormat is ffmpeg's muxer name ("mpegts", "mp4", "hls"). "hls" writes
	// a playlist plus rolling fMP4 segments into the process working directory
	// (see WithWorkDir) rather than a single stream on pipe:1.
	OutputFormat string

	// VideoEncoder re-encodes the video; nil stream-copies it. The encoder
	// carries its own device setup, filters, and flags, so EncodeArgs never
	// branches on the encoder kind. When SubtitleTextFile is set the planner
	// must supply one: drawtext needs decoded frames, so copy is not possible.
	VideoEncoder *Encoder

	// VideoBitrate target when re-encoding video (e.g. "4M"). Ignored when
	// VideoEncoder is nil (copy).
	VideoBitrate string

	// VideoMaxrate is the VBV peak-rate cap (e.g. "4M"), and VideoBufsize the
	// VBV buffer (e.g. "8M"). Together they bound the instantaneous bitrate so a
	// complex scene can't spike past what the renderer decodes and buffers. Both
	// empty leaves the encoder in unbounded ABR. Ignored when VideoEncoder is
	// nil (copy).
	VideoMaxrate string
	VideoBufsize string

	// VideoMaxHeight caps the output height while preserving aspect ratio.
	// 0 keeps the source height. Ignored when VideoEncoder is nil (copy).
	VideoMaxHeight int

	// KeyframeIntervalSec caps the GOP length in seconds via force_key_frames,
	// so a renderer joining mid-stream resyncs within this bound regardless of
	// source fps. 0 leaves the encoder default. Ignored when VideoEncoder is
	// nil (copy): a copied bitstream keeps the source's keyframes.
	KeyframeIntervalSec int

	// AudioCodec is CodecCopy or an encoder name like "aac".
	AudioCodec string

	// AudioBitrate target when re-encoding (e.g. "256k"). Ignored for copy.
	AudioBitrate string

	// AudioSampleRate target when re-encoding (Hz). 0 keeps the source rate.
	AudioSampleRate int

	// AudioChannels target when re-encoding. 0 keeps the source layout. The
	// planner's audio resolver sets this: 2 to downmix to stereo (the floor every
	// renderer decodes), or the source layout capped at the codec's ceiling to
	// keep 5.1/7.1. Ignored for copy.
	AudioChannels int

	// SubtitleTextFile, when non-empty, burns the file's current contents
	// into every frame via drawtext with reload=1: ffmpeg re-opens the file
	// by path before each frame, so an external writer can swap the active
	// subtitle line live (atomic rename only — a failed read kills ffmpeg).
	// The file must exist before ffmpeg starts. Forces a video re-encode.
	// Enabling this routes -progress to fd 3: start the process
	// WithExtraPipe and follow Process.Extra.
	SubtitleTextFile string
}

// EncodeReadrateBurstSeconds is how much of the stream the subtitle-burning
// encoder may race through at full speed before -readrate pins it to its
// steady pace. Exported because the playback gate's transcription lead must
// cover it: frames encoded during the burst need their cues committed before
// the encode starts.
const EncodeReadrateBurstSeconds = 10

// EncodeReadrate paces the subtitle-burning encoder just above realtime. It
// must not be exactly 1.0: at dead-even playback speed the renderer's buffer
// has no steady-state headroom, so any encode or network jitter permanently
// erodes the initial preroll, and because the encoder never runs ahead it can
// never rebuild it. A slight margin lets the encoder's output spool accumulate
// a lead the renderer can draw from. Stays well under the puller's 2x, so the
// encode never overtakes whisper's committed frontier (the gate guarantees a
// lead before playback opens).
const EncodeReadrate = "1.15"

// containerInputArgs returns the ffmpeg input flags a source container needs.
// HLS (and DASH) playlists require the extension checks relaxed; those flags
// are options on the HLS demuxer, so ffmpeg aborts a plain-file input (MP4,
// MKV) with "Option not found" when they are present. Direct files need none.
func containerInputArgs(contentType string) []string {
	switch contentType {
	case media.HLS:
		return media.HLSInputArgs
	default:
		return nil
	}
}

// pacing is how fast a reader consumes its input: an ffmpeg -readrate multiple
// of realtime, plus how many seconds it may take at wire speed first. The zero
// value is wire speed throughout.
type pacing struct{ readrate, burst string }

// The paces castor reads at. The first two are per source nature and shared by
// both network readers: VOD bursts at wire speed then reads at 2x realtime, which
// keeps a reader well ahead of 1x playback without pulling a whole movie in a
// couple of minutes; live can't be outrun, and the same burst only asks a CDN for
// segments that do not exist yet and trips its rate limiter.
var (
	pacingVOD  = pacing{readrate: "2.0", burst: "90"}
	pacingLive = pacing{readrate: "1.0", burst: "0"}
	// pacingHLSWindow is imposed by an output rather than a source: it feeds a
	// deleting HLS window (see hlsOutputArgs) at exactly wall-clock speed, not the
	// slight over-speed above, because the client consumes that window at 1x and
	// anything faster rolls the next-needed segment off the back before it asks.
	// The burst fills the window once so the device can prebuffer first.
	pacingHLSWindow = pacing{readrate: "1.0", burst: strconv.Itoa(hlsWindowSeconds)}
)

// args renders the pacing as ffmpeg input flags, or nothing when unpaced.
func (p pacing) args() []string {
	if p.readrate == "" {
		return nil
	}
	return []string{"-readrate", p.readrate, "-readrate_initial_burst", p.burst}
}

// pacing returns the read rate this source's nature calls for, for a reader that
// paces at all (see segmented).
func (s NetworkSource) pacing() pacing {
	if s.Live {
		return pacingLive
	}
	return pacingVOD
}

// segmented reports whether reading this source means many small segment
// requests rather than one long GET, which is what decides whether a reader free
// to run at wire speed should pace anyway: a two-hour HLS title read unpaced is
// thousands of requests in a couple of minutes, and CDNs answer that with 429s.
// One long GET of a single file (mp4/mkv/avi) is throttled by nothing.
func (s NetworkSource) segmented() bool { return s.ContentType == media.HLS }

// inputArgs renders the input side of a network read: the pacing, the
// reconnect policy every upstream fetch castor makes shares, the request
// headers, the container's input flags, and the URL. A demuxed program is two
// inputs read on identical terms, since its renditions are two halves of one
// program from one origin (see audioMap for the mapping that follows).
func (s NetworkSource) inputArgs(pace pacing) []string {
	args := s.input(pace, s.URL)
	if s.AudioURL != nil {
		args = append(args, s.input(pace, s.AudioURL)...)
	}
	return args
}

// input renders the flags for one of the source's inputs. ffmpeg applies these
// to the input that follows them, so every input of a demuxed program repeats
// them.
func (s NetworkSource) input(pace pacing, u *url.URL) []string {
	args := pace.args()
	args = append(args,
		"-rw_timeout", strconv.FormatInt(s.RWTimeout.Microseconds(), 10),
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		// A minute of backoff paired with 429-as-reconnect lets a rate-limited
		// CDN be waited out, instead of the HLS demuxer burning through segment
		// numbers that all fail and keeping the IP tarpitted.
		"-reconnect_delay_max", "60",
		"-reconnect_on_http_error", "429",
	)
	args = append(args, media.HeaderArgs(s.Headers)...)
	args = append(args, containerInputArgs(s.ContentType)...)
	return append(args, "-i", u.String())
}

// audioMap is the ffmpeg stream specifier for the source's audio: the second
// input when the program is demuxed, the first input's own audio otherwise. The
// video map is always 0:v:0, so this is the only specifier that moves.
//
// A pipe-fed encode leaves the source zero-valued and lands on 0:a:0, which is
// right: the spool it reads is a single muxed stream whatever the origin looked
// like.
func (s NetworkSource) audioMap() string {
	if s.AudioURL != nil {
		return "1:a:0"
	}
	return "0:a:0"
}

// EncodeArgs assembles the encode command line. No "magic" flags: every
// argument is either part of the standard input/output setup or comes
// straight from a field in EncodeOptions. It enforces the one cross-field
// contract EncodeOptions documents but can't express in its types: a
// SubtitleTextFile burn-in needs decoded frames, so it requires a real
// VideoEncoder rather than failing later inside ffmpeg with an unrelated
// "Filtering and streamcopy cannot be used together".
func EncodeArgs(opts EncodeOptions) ([]string, error) {
	if opts.SubtitleTextFile != "" && opts.VideoEncoder == nil {
		return nil, fmt.Errorf("subtitle burn-in requires a video re-encode: VideoEncoder is nil with SubtitleTextFile set")
	}

	// -nostats: the \r-terminated progress line never completes, so it
	// accumulates into one giant stderr "line" that drowns the tail buffer
	// real errors live in. Position tracking uses -progress instead.
	args := []string{"-hide_banner", "-nostats", "-fflags", "+genpts+discardcorrupt"}

	// A nil VideoEncoder stream-copies the video. Otherwise the encoder
	// contributes its own hardware-device setup (emitted before the input, so
	// both the upload filter and the encoder can reference it), filters, and
	// flags, so there is no per-encoder branching below.
	enc := opts.VideoEncoder
	if enc != nil {
		args = append(args, enc.InitArgs...)
	}

	if opts.PipeFormat != "" {
		if opts.SubtitleTextFile != "" {
			// Pace the encode to just above realtime. It must stay near
			// wall-clock speed: the cue writer swaps drawtext's textfile as
			// -progress ticks arrive, and unpaced the encoder rips through the
			// spool at CPU speed (every tick covering seconds of video, so cues
			// smear or skip) and overtakes the transcriber's commit frontier,
			// after which every cue lookup misses and subtitles stop. It must
			// not be exactly realtime either — see EncodeReadrate.
			args = append(args, pacing{
				readrate: EncodeReadrate,
				burst:    strconv.Itoa(EncodeReadrateBurstSeconds),
			}.args()...)
		}
		args = append(args, "-f", opts.PipeFormat, "-i", "pipe:0")
	} else {
		// This reader may run at wire speed, since its output is either
		// replay-spooled from byte 0 or a rolling window, so it paces only where
		// running fast would hurt:
		//
		//   - a deleting HLS output window (see hlsOutputArgs) must be produced at
		//     exactly wall-clock speed, or the window rolls segments off faster than
		//     the device plays them at 1x and only the tail is ever fetchable; its
		//     burst fills the window once so the device can prebuffer first;
		//   - a segmented source is read at the pace its nature calls for, since
		//     wire speed against a segmented CDN is a request storm (see segmented).
		var pace pacing
		switch {
		case opts.OutputFormat == hlsMuxer:
			pace = pacingHLSWindow
		case opts.Source.segmented():
			pace = opts.Source.pacing()
		}
		args = append(args, opts.Source.inputArgs(pace)...)
	}

	// Map the first video and first audio track explicitly. ffmpeg's default
	// stream selection picks the audio track with the most channels, which on a
	// multi-track source can differ from the first track the planner probed to
	// choose copy-vs-encode — so the encode would apply that decision to the
	// wrong track. Pinning the pair keeps the encoded track identical to the
	// probed one, and on a demuxed program it is what joins the two inputs back
	// into one output. (The read-once spool is already single-audio via the
	// puller's -map, so this only changes behaviour for the direct network remux.)
	args = append(args, "-map", "0:v:0", "-map", opts.Source.audioMap())

	// Video filter chain. scale= runs first so text is rendered at the final
	// resolution (crisper than scaling rendered text); it caps height while
	// keeping width divisible by 2 (encoder requirement) and preserving aspect
	// ratio via -2. The encoder's own filters (e.g. the VA-API GPU upload) come
	// last, after scale and drawtext have run on CPU frames. Copy skips all of
	// this (enc == nil): a copied bitstream can't be filtered.
	var vfilters []string
	if enc != nil && opts.VideoMaxHeight > 0 {
		vfilters = append(vfilters, fmt.Sprintf("scale=-2:'min(%d,ih)'", opts.VideoMaxHeight))
	}
	if opts.SubtitleTextFile != "" {
		vfilters = append(vfilters, drawtextFilter(opts.SubtitleTextFile))
	}
	if enc != nil {
		vfilters = append(vfilters, enc.Filters...)
	}
	if len(vfilters) > 0 {
		args = append(args, "-vf", strings.Join(vfilters, ","))
	}

	if enc == nil {
		args = append(args, "-c:v", CodecCopy)
	} else {
		args = append(args, "-c:v", enc.Name)
		args = append(args, enc.Flags...)
		if opts.VideoBitrate != "" {
			args = append(args, "-b:v", opts.VideoBitrate)
		}
		// VBV cap: bound the instantaneous bitrate so the pacer's fixed send
		// rate is a real ceiling. Both encoders honour this (libx264 VBV,
		// VideoToolbox/VA-API DataRateLimits).
		if opts.VideoMaxrate != "" {
			args = append(args, "-maxrate", opts.VideoMaxrate)
		}
		if opts.VideoBufsize != "" {
			args = append(args, "-bufsize", opts.VideoBufsize)
		}
		// Cap the GOP in wall-clock time, fps-independent, so a renderer that
		// joins mid-stream resyncs within the interval. Works on every encoder
		// family (VideoToolbox additionally needs -g in its Flags to lift its
		// wasteful sub-second default so this expression is the real limiter).
		if opts.KeyframeIntervalSec > 0 {
			args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", opts.KeyframeIntervalSec))
		}
	}

	args = append(args, "-c:a", opts.AudioCodec)
	if opts.AudioCodec != CodecCopy {
		if opts.AudioSampleRate > 0 {
			args = append(args, "-ar", strconv.Itoa(opts.AudioSampleRate))
		}
		if opts.AudioChannels > 0 {
			args = append(args, "-ac", strconv.Itoa(opts.AudioChannels))
		}
		if opts.AudioBitrate != "" {
			args = append(args, "-b:a", opts.AudioBitrate)
		}
	}

	switch opts.OutputFormat {
	case "mpegts":
		// mpegts container tuning. PCR and PAT/PMT need to repeat frequently
		// so a renderer that joins mid-stream (some smart TVs HEAD-probe then
		// GET before playing) can resync within one GOP. Annexb conversion for
		// copied video is auto-inserted by ffmpeg per actual codec when needed.
		args = append(args,
			"-mpegts_flags", "+resend_headers+initial_discontinuity",
			// pat_period (not "mpegts_pat_period" — that name was removed
			// in ffmpeg 8) caps the interval between PAT/PMT tables, which
			// renderers joining mid-stream need to resync within one GOP.
			"-pat_period", "0.1",
			"-muxdelay", "0",
			"-muxpreload", "0",
		)
	case "mp4":
		// Plain mp4 needs a seekable output to finalize the moov atom; on a
		// pipe we must fragment instead.
		args = append(args, "-movflags", "+frag_keyframe+empty_moov+default_base_moof")
	}

	if opts.SubtitleTextFile != "" {
		// Progress reporting drives the live subtitle writer: it tells us
		// the encoder's output position so the writer can swap the active
		// cue in the textfile. fd 3 is the runner's extra pipe. With the
		// encode paced at realtime, the period is also the cue placement
		// granularity in video time.
		args = append(args, "-progress", "pipe:3", "-stats_period", "0.1")
	}

	// HLS writes a playlist + segment files, not a stream on a pipe. The bare
	// relative filenames rely on the process running WithWorkDir(the cast dir).
	if opts.OutputFormat == hlsMuxer {
		return append(args, hlsOutputArgs()...), nil
	}
	args = append(args, "-f", opts.OutputFormat, "pipe:1")
	return args, nil
}

// HLS output tuning. A live sliding-window fMP4 tail: hlsListSize segments of
// ~hlsSegmentSeconds each keep hlsWindowSeconds on disk, comfortably above the
// ~30s-behind-live-edge window HLS clients conventionally buffer; hls_playlist_type
// stays unset so the window rolls (event/vod would pin the list size to 0 and
// grow disk unbounded).
const (
	// hlsMuxer is ffmpeg's HLS muxer name and the OutputFormat value selecting the
	// live-directory output (files under the work dir, not pipe:1).
	hlsMuxer = "hls"
	// hlsSegmentSeconds targets the segment length. With stream-copy the muxer can
	// only cut on a source keyframe, so it is a lower bound, not exact.
	hlsSegmentSeconds = 4
	// hlsListSize is how many segments the rolling playlist keeps on disk.
	hlsListSize = 8
	// hlsWindowSeconds is the on-disk window; it also sizes pacingHLSWindow's
	// burst so the device can prebuffer one full window before pacing binds.
	hlsWindowSeconds = hlsSegmentSeconds * hlsListSize
)

func hlsOutputArgs() []string {
	return []string{
		"-f", hlsMuxer,
		"-hls_time", strconv.Itoa(hlsSegmentSeconds),
		"-hls_list_size", strconv.Itoa(hlsListSize),
		"-hls_flags", "delete_segments+independent_segments",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", media.HLSInitName,
		"-hls_segment_filename", media.HLSSegmentPattern,
		media.HLSPlaylistName,
	}
}

// PullOptions configures the single upstream reader's command line.
type PullOptions struct {
	// Source is the upstream to download, read exactly as the served remux
	// reads its own (same reconnect policy, headers, container flags, pacing).
	Source NetworkSource

	// Verbose selects -loglevel verbose (playlist/segment URLs, connection
	// lines) instead of the default warning level.
	Verbose bool

	// PCM additionally extracts mono s16le audio on fd 3 for the
	// transcriber; start the process WithExtraPipe.
	PCM bool
	// PCMSampleRate is the audio sample rate for the PCM output.
	PCMSampleRate int
}

// PullArgs assembles the upstream download command line: a codec-copy remux
// of the source into append-only MPEG-TS on stdout, paced like a buffering
// player, with an optional PCM tee for transcription.
func PullArgs(opts PullOptions) []string {
	// Baseline "warning" (not "error") so HLS segment failures — "Failed to open
	// segment N", "HTTP error 404 Not Found" — reach the stderr ring tail.
	// They're warning-level in ffmpeg, so -loglevel error hides them, and a pull
	// whose every segment 404s (expired signed URL) then looks identical to a
	// silent stall: the playback gate reports "throttled or expired" with no
	// evidence. Capturing them lets the gate show the real reason without
	// --debug. Under --debug, verbose additionally streams the playlist/segment
	// URLs and connection lines so a stall can be reproduced by hand.
	logLevel := "warning"
	if opts.Verbose {
		logLevel = "verbose"
	}

	args := []string{"-nostats", "-loglevel", logLevel}

	// The pull always paces, whatever the container: it buffers a whole title
	// into a spool the encoder tails, so running further ahead than the source's
	// own pace buys nothing and only spends the origin's patience.
	args = append(args, opts.Source.inputArgs(opts.Source.pacing())...)

	// Output 1: codec-copy remux to MPEG-TS on stdout → spool. mpegts is the
	// right spool format because it is strictly append-only (no trailer or
	// header seek-back), so a tail can read it while it grows. No explicit
	// bitstream filter: ffmpeg auto-inserts the right *_mp4toannexb for the
	// actual codec (h264 vs hevc) when the source uses fmp4 segments;
	// hardcoding the h264 one breaks HEVC sources.
	args = append(args,
		"-map", "0:v:0", "-map", opts.Source.audioMap(),
		"-c", CodecCopy,
		"-f", "mpegts", "pipe:1",
	)

	if opts.PCM {
		// Output 2: mono PCM for whisper on fd 3 (the runner's extra pipe).
		args = append(args,
			"-map", opts.Source.audioMap(), "-vn",
			"-ac", "1",
			"-ar", strconv.Itoa(opts.PCMSampleRate),
			"-f", "s16le", "pipe:3",
		)
	}
	return args
}

// drawtextFilter renders subtitle text bottom-centered with a translucent
// box, matching how dedicated subtitle renderers style their output.
// reload=1 makes ffmpeg re-open the textfile before every frame, which is
// what turns a static filter into a live subtitle track.
func drawtextFilter(textFile string) string {
	return strings.Join([]string{
		"drawtext=textfile=" + escapeFilterArg(textFile),
		"reload=1",
		"fontsize=h/24",
		"fontcolor=white",
		"borderw=2",
		"bordercolor=black",
		"box=1",
		"boxcolor=black@0.45",
		"boxborderw=10",
		"text_align=center",
		"line_spacing=6",
		"x=(w-text_w)/2",
		"y=h-text_h-(h/20)",
	}, ":")
}

// escapeFilterArg escapes a value for passing through ffmpeg's two-level
// filter-string parser (graph parser, then per-filter option parser). Each
// level consumes one backslash, so a literal ':' in a filter option needs
// '\\:' in the input — one backslash survives the graph parser and the
// next is consumed by the option parser. Single-quote wrapping at graph
// level does NOT propagate to the option parser, so we don't rely on it.
func escapeFilterArg(s string) string {
	r := strings.NewReplacer(
		`\`, `\\\\`, // four backslashes in source → two in the arg → one survives both parsers
		`:`, `\\:`, // two backslashes + colon → one + colon after graph → literal colon after option parser
		`'`, `\\'`,
		`,`, `\\,`,
	)
	return r.Replace(s)
}
