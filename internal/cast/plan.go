package cast

import (
	"fmt"
	"net/http"
	"net/url"

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

	// SubtitleDelivery says how subtitles reach the renderer, if at all.
	SubtitleDelivery SubtitleDelivery

	// Live marks a live-edge source; the puller paces at realtime.
	Live bool
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
	SourceLive        bool

	// MaxHeight caps the re-encode output height (the user's cast resolution
	// preference); 0 keeps the source height.
	MaxHeight    int
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
func BuildPlan(in PlanInput) (Plan, error) {
	switch in.DeviceType {
	case device.TypeDLNA:
		return planDLNA(in), nil
	case device.TypeChromecast:
		return planChromecast(in), nil
	}
	return Plan{}, fmt.Errorf("unknown device type: %q", in.DeviceType)
}

const (
	// dlnaKeyframeSeconds caps the encoded GOP so a renderer joining mid-stream
	// resyncs within a couple seconds.
	dlnaKeyframeSeconds = 2
	// dlnaAudioBitrate is the AAC re-encode target.
	dlnaAudioBitrate = "256k"
)

// videoTarget is a VBV-capped bitrate: the average, the peak cap, and the buffer
// window the cap applies over.
type videoTarget struct{ bitrate, maxrate, bufsize string }

// dlnaVideoTargets is the re-encode target per codec, bounding the transcoder's
// output so it stays within the renderer's decode budget. HEVC needs about half
// of H.264 for the same quality. maxrate == bitrate makes the VBV cap a true
// ceiling rather than an average the encoder overshoots; bufsize is ~2s. Adding
// a codec the pipeline encodes to is one entry here.
var dlnaVideoTargets = map[media.Codec]videoTarget{
	media.CodecH264: {bitrate: "4M", maxrate: "4M", bufsize: "8M"},
	media.CodecHEVC: {bitrate: "2M", maxrate: "2M", bufsize: "4M"},
}

func planDLNA(in PlanInput) Plan {
	p := Plan{
		SourceURL:         in.SourceURL,
		SourceHeaders:     in.SourceHeaders,
		SourceContentType: in.SourceContentType,
		OutputContentType: "video/mp2t",
		Live: in.SourceLive,
		// Transcode carries the codec-independent output targets (container,
		// audio, height, GOP). VideoEncoder and the video bitrate are left unset:
		// copy-vs-encode and the codec need the source's actual codec/profile
		// (known only after the spool has bytes to probe, since probing the
		// upstream signed URL would burn a short-lived token) and the renderer's
		// capabilities, so the pipeline resolves them from a local spool probe.
		Transcode: &ffmpeg.EncodeOptions{
			OutputFormat:        "mpegts",
			VideoMaxHeight:      in.MaxHeight,
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
			Live:              in.SourceLive,
		}
	}

	return Plan{
		SourceURL:         in.SourceURL,
		SourceHeaders:     in.SourceHeaders,
		SourceContentType: in.SourceContentType,
		OutputContentType: media.MP4,
		Live:              in.SourceLive,
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
