package dlna

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"

	"github.com/huin/goupnp"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// capsTimeout: ConnectionManager timeout; degrades rather than stall.
const capsTimeout = 3 * time.Second

// negotiateCaps asks the device what it accepts over ConnectionManager GetProtocolInfo.
func negotiateCaps(ctx context.Context, loc *goupnp.RootDevice, u *url.URL) *mediav1.Capabilities {
	manager, err := findService(loc, u, "ConnectionManager")
	if err != nil {
		slog.WarnContext(ctx, "no ConnectionManager service; using conservative capabilities", "error", err)
		return fallbackCaps()
	}
	ctx, cancel := context.WithTimeout(ctx, capsTimeout)
	defer cancel()

	response := &struct{ Source, Sink string }{}
	if err := manager.SOAPClient.PerformActionCtx(
		ctx, manager.Service.ServiceType, "GetProtocolInfo", nil, response); err != nil {
		slog.WarnContext(ctx, "GetProtocolInfo failed; using conservative capabilities", "error", err)
		return fallbackCaps()
	}
	sink := response.Sink
	caps := parseSinkProtocolInfo(sink)
	if len(caps.Video) == 0 {
		slog.WarnContext(ctx, "device advertised no known video codec; using conservative capabilities")
		return fallbackCaps()
	}
	// A nil Audio is not a device that plays silence, it is one that said nothing about audio.
	if len(caps.Audio) == 0 {
		slog.WarnContext(ctx, "device advertised no known audio codec; assuming the conservative audio floor",
			"codecs", codecs(caps.Video))
		caps.Audio = fallbackCaps().Audio
	}
	slog.InfoContext(ctx, "negotiated device capabilities", "codecs", codecs(caps.Video), "containers", caps.Containers)
	return caps
}

// audioSupportFor builds the copy envelope for an audio codec.
func audioSupportFor(codec mediav1.AudioCodec) *mediav1.AudioSupport {
	if codec == mediav1.AudioCodec_AUDIO_CODEC_AAC {
		return &mediav1.AudioSupport{Codec: codec, MaxChannels: 2}
	}
	return &mediav1.AudioSupport{Codec: codec}
}

// The fixed order capabilities are reported in, so a given Sink always yields the same record.
var (
	discoverableCodecs      = []mediav1.VideoCodec{mediav1.VideoCodec_VIDEO_CODEC_H264, mediav1.VideoCodec_VIDEO_CODEC_HEVC}
	discoverableAudioCodecs = []mediav1.AudioCodec{mediav1.AudioCodec_AUDIO_CODEC_AAC, mediav1.AudioCodec_AUDIO_CODEC_AC3, mediav1.AudioCodec_AUDIO_CODEC_EAC3}
	discoverableContainers  = []mediav1.Container{mediav1.Container_CONTAINER_MPEGTS, mediav1.Container_CONTAINER_MP4}
)

func fallbackCaps() *mediav1.Capabilities {
	return &mediav1.Capabilities{
		Containers:      []mediav1.Container{mediav1.Container_CONTAINER_MPEGTS},
		Video:           []*mediav1.VideoSupport{device.VideoSupport(mediav1.VideoCodec_VIDEO_CODEC_H264)},
		Audio:           []*mediav1.AudioSupport{audioSupportFor(mediav1.AudioCodec_AUDIO_CODEC_AAC)},
		ServedContainer: servedContainer,
		Deinterlaces:    true,
	}
}

// servedContainer is what castor muxes for a DLNA media.
const servedContainer = mediav1.Container_CONTAINER_MPEGTS

// parseSinkProtocolInfo maps a ConnectionManager Sink protocolInfo CSV into capabilities.
func parseSinkProtocolInfo(sink string) *mediav1.Capabilities {
	present := map[mediav1.VideoCodec]bool{}
	audioPresent := map[mediav1.AudioCodec]bool{}
	containers := map[mediav1.Container]bool{}
	for entry := range strings.SplitSeq(sink, ",") {
		fields := strings.SplitN(strings.TrimSpace(entry), ":", 4)
		if len(fields) < 3 || !strings.EqualFold(fields[0], "http-get") {
			continue
		}
		mime := strings.ToLower(fields[2])
		info := ""
		if len(fields) == 4 {
			info = strings.ToUpper(fields[3])
		}
		if c, ok := codecFromProfile(mime, info); ok {
			present[c] = true
		}
		if c, ok := audioFromProfile(mime, info); ok {
			audioPresent[c] = true
		}
		if ct, ok := containerFromMIME(mime); ok {
			containers[ct] = true
		}
	}

	// A DLNA device deinterlaces: broadcast reaches it as fields.
	r := &mediav1.Capabilities{ServedContainer: servedContainer, Deinterlaces: true}
	for _, c := range discoverableCodecs {
		if present[c] {
			r.Video = append(r.Video, device.VideoSupport(c))
		}
	}
	for _, c := range discoverableAudioCodecs {
		if audioPresent[c] {
			r.Audio = append(r.Audio, audioSupportFor(c))
		}
	}
	for _, ct := range discoverableContainers {
		if containers[ct] {
			r.Containers = append(r.Containers, ct)
		}
	}
	return r
}

// codecFromProfile reads the DLNA.ORG_PN token (already upper-cased), failing that the MIME type.
func codecFromProfile(mime, pn string) (mediav1.VideoCodec, bool) {
	switch {
	case strings.Contains(pn, "HEVC") || strings.Contains(pn, "H265") || strings.Contains(mime, "hevc") || strings.Contains(mime, "h265"):
		return mediav1.VideoCodec_VIDEO_CODEC_HEVC, true
	case strings.Contains(pn, "AVC") || strings.Contains(pn, "H264") || strings.Contains(mime, "avc") || strings.Contains(mime, "h264"):
		return mediav1.VideoCodec_VIDEO_CODEC_H264, true
	}
	return mediav1.VideoCodec_VIDEO_CODEC_UNSPECIFIED, false
}

func audioFromProfile(mime, pn string) (mediav1.AudioCodec, bool) {
	switch {
	case strings.Contains(pn, "EAC3") || strings.Contains(mime, "eac3") || strings.Contains(mime, "dd+"):
		return mediav1.AudioCodec_AUDIO_CODEC_EAC3, true
	case strings.Contains(pn, "AC3") || strings.Contains(mime, "ac3") || strings.Contains(mime, "dolby.dd"):
		return mediav1.AudioCodec_AUDIO_CODEC_AC3, true
	case strings.Contains(pn, "AAC") || strings.Contains(mime, "aac") || mime == "audio/mp4":
		return mediav1.AudioCodec_AUDIO_CODEC_AAC, true
	}
	return mediav1.AudioCodec_AUDIO_CODEC_UNSPECIFIED, false
}

// containerFromMIME reads the DLNA MIME spellings of the containers castor serves.
func containerFromMIME(mime string) (mediav1.Container, bool) {
	switch mime {
	case "video/mp2t", "video/mpeg", "video/vnd.dlna.mpeg-tts", "video/x-mpegts":
		return mediav1.Container_CONTAINER_MPEGTS, true
	case "video/mp4":
		return mediav1.Container_CONTAINER_MP4, true
	}
	return mediav1.Container_CONTAINER_UNSPECIFIED, false
}

// codecs names the codec of each support, video or audio.
func codecs[C interface {
	mediav1.VideoCodec | mediav1.AudioCodec
}, S interface{ GetCodec() C }](supports []S) []C {
	out := make([]C, len(supports))
	for i, s := range supports {
		out[i] = s.GetCodec()
	}
	return out
}
