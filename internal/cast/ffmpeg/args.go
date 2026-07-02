package ffmpeg

import (
	"fmt"
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
	SourceHeaders map[string]string

	// RWTimeoutMicros is the upstream I/O timeout in microseconds, passed to
	// ffmpeg's -rw_timeout for HTTP(S) input. Ignored for stdin input.
	RWTimeoutMicros int64

	// OutputFormat is ffmpeg's muxer name ("mpegts", "mp4").
	OutputFormat string

	// VideoCodec is "copy" for passthrough or an encoder name like "libx264".
	VideoCodec string

	// VideoPreset is the encoder preset ("veryfast"). Empty keeps the
	// encoder default. Ignored when VideoCodec is "copy".
	VideoPreset string

	// VideoBitrate target when re-encoding video (e.g. "4M"). Ignored when
	// VideoCodec is "copy".
	VideoBitrate string

	// VideoMaxHeight caps the output height while preserving aspect ratio.
	// 0 keeps the source height. Ignored when VideoCodec is "copy".
	VideoMaxHeight int

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

	// SubtitleFontFile is the absolute path of the font used by drawtext.
	// Required when SubtitleTextFile is set.
	SubtitleFontFile string
}

// EncodeReadrateBurstSeconds is how much of the stream the subtitle-burning
// encoder may race through at full speed before -readrate pins it to
// realtime. Exported because the playback gate's transcription lead must
// cover it: frames encoded during the burst need their cues committed before
// the encode starts.
const EncodeReadrateBurstSeconds = 10

// EncodeArgs assembles the encode command line. No "magic" flags: every
// argument is either part of the standard input/output setup or comes
// straight from a field in EncodeOptions.
func EncodeArgs(opts EncodeOptions) []string {
	// -nostats: the \r-terminated progress line never completes, so it
	// accumulates into one giant stderr "line" that drowns the tail buffer
	// real errors live in. Position tracking uses -progress instead.
	args := []string{"-hide_banner", "-nostats", "-fflags", "+genpts+discardcorrupt"}

	if opts.PipeFormat != "" {
		if opts.SubtitleTextFile != "" {
			// The cue writer swaps drawtext's textfile on wall-clock ticks
			// (-progress + -stats_period), so text lands on the right frames
			// only if encoding advances at wall-clock speed. Unpaced, the
			// encoder rips through the spool at CPU speed: every tick then
			// covers seconds of video (cues smear or are skipped outright)
			// and the encode overtakes the transcriber's commit frontier,
			// after which every cue lookup misses and subtitles stop.
			args = append(args,
				"-readrate", "1.0",
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
		if h := media.FormatHTTPHeaders(opts.SourceHeaders); h != "" {
			args = append(args, "-headers", h)
		}
		args = append(args, media.HLSInputArgs...)
		args = append(args, "-i", opts.SourceURL.String())
	}

	// Burning subtitles forces a video re-encode — drawtext operates on
	// decoded frames, so "copy" is not an option here.
	videoCodec := opts.VideoCodec
	if opts.SubtitleTextFile != "" && videoCodec == "copy" {
		videoCodec = "libx264"
	}

	// Video filter chain. scale= runs first so text is rendered at the
	// final resolution (crisper than scaling rendered text); it caps height
	// while keeping width divisible by 2 (libx264 requirement) and
	// preserving aspect ratio via -2.
	var vfilters []string
	if videoCodec != "copy" && opts.VideoMaxHeight > 0 {
		vfilters = append(vfilters, fmt.Sprintf("scale=-2:'min(%d,ih)'", opts.VideoMaxHeight))
	}
	if opts.SubtitleTextFile != "" {
		vfilters = append(vfilters, drawtextFilter(opts.SubtitleTextFile, opts.SubtitleFontFile))
	}
	if len(vfilters) > 0 {
		args = append(args, "-vf", strings.Join(vfilters, ","))
	}

	args = append(args, "-c:v", videoCodec)
	if videoCodec != "copy" {
		if opts.VideoPreset != "" {
			args = append(args, "-preset", opts.VideoPreset)
		}
		if opts.VideoBitrate != "" {
			args = append(args, "-b:v", opts.VideoBitrate)
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

// PullOptions configures the single upstream reader's command line.
type PullOptions struct {
	SourceURL       *url.URL
	SourceHeaders   map[string]string
	RWTimeoutMicros int64

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
	args := []string{
		"-loglevel", logLevel,
		"-rw_timeout", strconv.FormatInt(opts.RWTimeoutMicros, 10),
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
		// Pace the download like a buffering player: an initial burst at
		// full speed (fills the transcription lead so the playback gate
		// opens in seconds) then 2x realtime. An unpaced pull rips the
		// whole movie at wire speed, which streaming CDNs flag as a bot —
		// observed as an IP-level tarpit that breaks subsequent casts.
		"-readrate", "2.0",
		"-readrate_initial_burst", "90",
	}
	if h := media.FormatHTTPHeaders(opts.SourceHeaders); h != "" {
		args = append(args, "-headers", h)
	}
	args = append(args, media.HLSInputArgs...)
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
func drawtextFilter(textFile, fontFile string) string {
	return strings.Join([]string{
		"drawtext=textfile=" + escapeFilterArg(textFile),
		"reload=1",
		"fontfile=" + escapeFilterArg(fontFile),
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
