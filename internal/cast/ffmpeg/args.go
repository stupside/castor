package ffmpeg

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/stupside/castor/internal/media"
)

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

	// SourceURL is the network input, used when PipeFormat is empty.
	SourceURL *url.URL

	// SourceHeaders are HTTP headers ffmpeg sends when fetching SourceURL
	// (Referer, User-Agent, Cookie, etc. — needed for HLS behind proxies).
	SourceHeaders http.Header

	// SourceContentType is the MIME type of SourceURL. It selects the
	// container-specific input flags (see containerInputArgs). Only consulted
	// for a network source (PipeFormat empty).
	SourceContentType string

	// RWTimeoutMicros is the upstream I/O timeout in microseconds, passed to
	// ffmpeg's -rw_timeout for HTTP(S) input. Ignored for stdin input.
	RWTimeoutMicros int64

	// OutputFormat is ffmpeg's muxer name ("mpegts", "mp4").
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

	// AudioCodec is "copy" or an encoder name like "aac".
	AudioCodec string

	// AudioBitrate target when re-encoding (e.g. "256k"). Ignored for copy.
	AudioBitrate string

	// AudioSampleRate target when re-encoding (Hz). 0 keeps the source rate.
	AudioSampleRate int

	// AudioChannels target when re-encoding. 0 keeps the source layout.
	// Set to 2 for DLNA so Samsung firmwares accept the stream.
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

// EncodeArgs assembles the encode command line. No "magic" flags: every
// argument is either part of the standard input/output setup or comes
// straight from a field in EncodeOptions.
func EncodeArgs(opts EncodeOptions) []string {
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
			args = append(args,
				"-readrate", EncodeReadrate,
				"-readrate_initial_burst", strconv.Itoa(EncodeReadrateBurstSeconds),
			)
		}
		args = append(args, "-f", opts.PipeFormat, "-i", "pipe:0")
	} else {
		args = append(args,
			"-rw_timeout", strconv.FormatInt(opts.RWTimeoutMicros, 10),
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_delay_max", "5",
		)
		args = append(args, media.HeaderArgs(opts.SourceHeaders)...)
		args = append(args, containerInputArgs(opts.SourceContentType)...)
		args = append(args, "-i", opts.SourceURL.String())
	}

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
		args = append(args, "-c:v", "copy")
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
	if opts.AudioCodec != "copy" {
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
		// so a renderer that joins mid-stream (Samsung's HEAD-probe-then-play
		// pattern) can resync within one GOP. Annexb conversion for copied
		// video is auto-inserted by ffmpeg per actual codec when needed.
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

	args = append(args, "-f", opts.OutputFormat, "pipe:1")
	return args
}

// Pull pacing per source nature. VOD bursts at wire speed then 2x
// realtime. Live can't be outrun — the same burst trips rate limits.
const (
	pullReadrateVOD       = "2.0"
	pullReadrateBurstVOD  = "90"
	pullReadrateLive      = "1.0"
	pullReadrateBurstLive = "0"
)

// PullOptions configures the single upstream reader's command line.
type PullOptions struct {
	SourceURL         *url.URL
	SourceHeaders     http.Header
	SourceContentType string // MIME type of SourceURL; selects container-specific input flags
	RWTimeoutMicros   int64

	// Verbose selects -loglevel verbose (playlist/segment URLs, connection
	// lines) instead of the default warning level.
	Verbose bool

	// PCM additionally extracts mono s16le audio on fd 3 for the
	// transcriber; start the process WithExtraPipe.
	PCM bool
	// PCMSampleRate is the audio sample rate for the PCM output.
	PCMSampleRate int

	// Live selects live pacing (see pullReadrate constants) instead of VOD.
	Live bool
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

	args := []string{
		"-loglevel", logLevel,
		"-rw_timeout", strconv.FormatInt(opts.RWTimeoutMicros, 10),
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		// Treat HTTP 429 as a reconnect trigger so ffmpeg's exponential
		// backoff (capped at 60 s) absorbs rate limiting instead of the
		// HLS demuxer spinning through failed segment numbers at wire
		// speed and keeping the IP tarpitted.
		"-reconnect_delay_max", "60",
		"-reconnect_on_http_error", "429",
	}

	readrate, burst := pullReadrateVOD, pullReadrateBurstVOD
	if opts.Live {
		readrate, burst = pullReadrateLive, pullReadrateBurstLive
	}
	args = append(args, "-readrate", readrate, "-readrate_initial_burst", burst)

	args = append(args, media.HeaderArgs(opts.SourceHeaders)...)
	args = append(args, containerInputArgs(opts.SourceContentType)...)
	args = append(args, "-i", opts.SourceURL.String())

	// Output 1: codec-copy remux to MPEG-TS on stdout → spool. mpegts is the
	// right spool format because it is strictly append-only (no trailer or
	// header seek-back), so a tail can read it while it grows. No explicit
	// bitstream filter: ffmpeg auto-inserts the right *_mp4toannexb for the
	// actual codec (h264 vs hevc) when the source uses fmp4 segments;
	// hardcoding the h264 one breaks HEVC sources.
	args = append(args,
		"-map", "0:v:0", "-map", "0:a:0",
		"-c", "copy",
		"-f", "mpegts", "pipe:1",
	)

	if opts.PCM {
		// Output 2: mono PCM for whisper on fd 3 (the runner's extra pipe).
		args = append(args,
			"-map", "0:a:0", "-vn",
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
