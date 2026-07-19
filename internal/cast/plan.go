package cast

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// Plan captures every decision the cast pipeline makes up front, so the
// executor has nothing to guess. The planner is the only place that branches
// on device type or source media properties; everything downstream just
// follows the plan.
type Plan struct {
	// SourceURL is what ffmpeg / the device will read from. For Chromecast
	// pass-through it's the upstream URL directly; for transcoded paths it's
	// the local stream server's URL.
	SourceURL *url.URL

	// SourceHeaders are the HTTP headers ffmpeg needs to fetch SourceURL when
	// the source is HLS behind a proxy/CDN that requires them.
	SourceHeaders http.Header

	// SourceContentType is the resolved MIME type of SourceURL. The puller
	// applies HLS-only ffmpeg input flags solely to HLS sources.
	SourceContentType string

	// OutputContentType is the MIME type advertised to the device (e.g.
	// "video/mp2t" for mpegts).
	OutputContentType string

	// Transcode is non-nil when we need to spawn ffmpeg between source and
	// device. Pass-through (e.g. Chromecast accepting HLS) leaves it nil.
	Transcode *ffmpeg.EncodeOptions

	// Spool routes the cast through the read-once pipeline: a single puller
	// downloads the source into an on-disk spool (feeding whisper along the
	// way) and the encoder reads the spool instead of the network. The
	// upstream URL is touched by exactly one connection, and playback
	// survives any CDN behavior once bytes are local.
	Spool bool

	// SubtitleDelivery says how subtitles reach the renderer, if at all.
	SubtitleDelivery SubtitleDelivery

	// SendRate is the target byte/sec for the HTTP stream server. 0 disables
	// pacing.
	SendRate int64

	// SendBurst is the initial unrestricted byte budget before pacing kicks
	// in — gives the renderer enough preroll to leave "loading" without
	// over-stuffing its internal buffer.
	SendBurst int64
}

// SubtitleDelivery is how the planner intends to get subtitles on screen.
type SubtitleDelivery int

const (
	SubtitleNone SubtitleDelivery = iota
	SubtitleHardsub
)

func (d SubtitleDelivery) String() string {
	if d == SubtitleHardsub {
		return "hardsub"
	}
	return "none"
}

// PlanInput is everything the planner needs to make decisions.
type PlanInput struct {
	DeviceType device.Type
	Renderer   media.Renderer

	SourceURL         *url.URL
	SourceHeaders     http.Header
	SourceContentType string
	SourceBitRate     int64 // 0 if unknown

	HasSubtitles bool
}

// BuildPlan turns PlanInput into a Plan. Per-device rules:
//
//   - DLNA: always spool (read-once pipeline) and remux to MPEG-TS. The video
//     is stream-copied when the source is an envelope the renderer decodes
//     natively, else re-encoded (a hardware encoder if one works on this host,
//     else libx264); the pipeline decides from a local spool probe. Audio is
//     always re-encoded to AAC. Subtitles are burned in (drawtext) when enabled:
//     Samsung renderers can't be trusted to display DLNA-delivered caption
//     tracks, sidecar or in-band.
//
//   - Chromecast: pass the source through when the device accepts its container;
//     otherwise remux (video copied, audio to AAC). Cast's own buffering handles
//     pacing so we don't impose any.
func BuildPlan(in PlanInput) Plan {
	switch in.DeviceType {
	case device.TypeDLNA:
		return planDLNA(in)
	case device.TypeChromecast:
		return planChromecast(in)
	}
	return Plan{
		SourceURL:         in.SourceURL,
		SourceHeaders:     in.SourceHeaders,
		SourceContentType: in.SourceContentType,
		OutputContentType: in.SourceContentType,
	}
}

const (
	// dlnaVideoBitrate is the libx264 target. Kept modest so a 1080p stream
	// fits comfortably in most TV media buffers without overflowing.
	dlnaVideoBitrate = "4M"
	// dlnaVideoMaxrate/dlnaVideoBufsize cap the instantaneous bitrate (VBV) at
	// the average, so the stream never spikes above the pacer's send rate and
	// starves the renderer on a complex scene. bufsize ~2s at 4M; the pacer's
	// preroll (dlnaPrerollSeconds) comfortably covers a full VBV excursion.
	dlnaVideoMaxrate = "4M"
	dlnaVideoBufsize = "8M"
	// dlnaKeyframeSeconds caps the encoded GOP so a renderer joining mid-stream
	// resyncs within a couple seconds.
	dlnaKeyframeSeconds = 2
	// dlnaAudioBitrate is the AAC re-encode target.
	dlnaAudioBitrate = "256k"
	// dlnaPrerollSeconds is how much of the encoded output the renderer is
	// allowed to gulp before the token bucket engages. Just enough to leave
	// the "loading" state — going higher only lets the TV buffer ahead of
	// playback rate, which is exactly what overflows its internal ring.
	dlnaPrerollSeconds = 4
	// dlnaPaceHeadroomPct is how much faster than the encoded rate we send
	// in steady state. Slightly above playback so the renderer's buffer
	// stays full but doesn't grow.
	dlnaPaceHeadroomPct = 5
)

func planDLNA(in PlanInput) Plan {
	sendRate, sendBurst := dlnaPacing(encodedBitrateBPS(dlnaVideoBitrate, dlnaAudioBitrate))
	p := Plan{
		SourceURL:         in.SourceURL,
		SourceHeaders:     in.SourceHeaders,
		SourceContentType: in.SourceContentType,
		OutputContentType: "video/mp2t",
		SendRate:          sendRate,
		SendBurst:         sendBurst,
		Spool:             true,
		// Transcode carries the fixed output targets (container, audio, video
		// bitrate and height). VideoEncoder is left unset here: copy-vs-encode
		// needs the source's actual codec/profile/level, known only after the
		// spool has bytes to probe (probing the upstream signed URL would burn a
		// short-lived token), so the pipeline resolves VideoEncoder and pacing
		// from a local spool probe.
		Transcode: &ffmpeg.EncodeOptions{
			OutputFormat:        "mpegts",
			VideoBitrate:        dlnaVideoBitrate,
			VideoMaxrate:        dlnaVideoMaxrate,
			VideoBufsize:        dlnaVideoBufsize,
			VideoMaxHeight:      1080,
			KeyframeIntervalSec: dlnaKeyframeSeconds,
			AudioCodec:          "aac",
			AudioBitrate:        dlnaAudioBitrate,
			AudioSampleRate:     48000,
			AudioChannels:       2,
		},
	}
	if in.HasSubtitles {
		p.SubtitleDelivery = SubtitleHardsub
	}
	return p
}

// dlnaPacing turns the output bit rate into the HTTP server's token-bucket
// settings: a steady send rate slightly above playback, and an initial burst
// sized to leave the renderer's "loading" state without over-filling its ring.
func dlnaPacing(encodedBitsPerSec int64) (sendRate, sendBurst int64) {
	return encodedBitsPerSec * (100 + dlnaPaceHeadroomPct) / 100 / 8,
		encodedBitsPerSec * dlnaPrerollSeconds / 8
}

func planChromecast(in PlanInput) Plan {
	// Subtitle delivery on Chromecast would naturally be a native text track
	// attached to the Load message, but the vishen/go-chromecast library
	// doesn't expose tracks on MediaItem and has no public Send for a custom
	// LoadMediaCommand. Until that's resolved the Chromecast path ships
	// without subtitles; the planner leaves SubtitleDelivery at None.

	if in.Renderer.AcceptsContainer(in.SourceContentType) {
		return Plan{
			SourceURL:         in.SourceURL,
			SourceHeaders:     in.SourceHeaders,
			SourceContentType: in.SourceContentType,
			OutputContentType: in.SourceContentType,
		}
	}

	return Plan{
		SourceURL:         in.SourceURL,
		SourceHeaders:     in.SourceHeaders,
		SourceContentType: in.SourceContentType,
		OutputContentType: media.MP4,
		Transcode: &ffmpeg.EncodeOptions{
			OutputFormat:      "mp4",
			SourceContentType: in.SourceContentType,
			// VideoEncoder unset (nil) stream-copies the video: Chromecast
			// decodes the source codec, only the container changes to mp4.
			AudioCodec:      "aac",
			AudioBitrate:    "256k",
			AudioSampleRate: 48000,
			AudioChannels:   2,
		},
	}
}

// encodedBitrateBPS turns ffmpeg-style bitrate strings ("4M", "256k") into
// bits per second and sums them. Returns 0 on a bad string (caller should
// just fall back to skipping rate calculations).
func encodedBitrateBPS(video, audio string) int64 {
	return parseBitrate(video) + parseBitrate(audio)
}

func parseBitrate(s string) int64 {
	if s == "" {
		return 0
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k', 'K':
		mult = 1_000
		s = s[:len(s)-1]
	case 'm', 'M':
		mult = 1_000_000
		s = s[:len(s)-1]
	case 'g', 'G':
		mult = 1_000_000_000
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}
