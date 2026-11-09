package dlna

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/huin/goupnp"
	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// capsTimeout bounds the ConnectionManager question a Connect asks.
const capsTimeout = 3 * time.Second

// sinkEntry is how the device itself names a container it accepts: the MIME and fourth field of its sink protocolInfo.
type sinkEntry struct {
	mime     string
	features string
}

// negotiateCaps asks the device what it accepts over ConnectionManager GetProtocolInfo.
// Renderers without usable answers retain the conservative MPEG-TS path from 1.9.x.
func negotiateCaps(ctx context.Context, loc *goupnp.RootDevice, u *url.URL) (*mediav1.Capabilities, map[mediav1.Container]sinkEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	manager, err := findService(loc, u, "ConnectionManager")
	if err != nil {
		slog.WarnContext(ctx, "DLNA ConnectionManager unavailable; using conservative capabilities", "error", err)
		caps, sinks := fallbackCaps()
		return caps, sinks, nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, capsTimeout)
	defer cancel()

	response := &struct{ Source, Sink string }{}
	if err := manager.SOAPClient.PerformActionCtx(
		queryCtx, manager.Service.ServiceType, "GetProtocolInfo", nil, response); err != nil {
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("asking the device what it plays: %w", err)
		}
		slog.WarnContext(ctx, "DLNA GetProtocolInfo failed; using conservative capabilities", "error", err)
		caps, sinks := fallbackCaps()
		return caps, sinks, nil
	}
	caps, sinks := parseSinkProtocolInfo(response.Sink)
	if len(caps.Formats) == 0 {
		slog.WarnContext(ctx, "DLNA sink contains no supported playback envelope; using conservative capabilities")
		caps, sinks := fallbackCaps()
		return caps, sinks, nil
	}
	slog.InfoContext(ctx, "negotiated device capabilities", "formats", len(caps.Formats), "containers", containers(caps))

	return caps, sinks, nil
}

// containers names each container the device accepts progressively, in preference order.
func containers(caps *mediav1.Capabilities) []string {
	var out []string
	for _, f := range caps.Formats {
		out = append(out, f.GetProgressive().GetContainer().String())
	}
	return slices.Compact(out)
}

// fallbackCaps provides one valid stream for renderers whose sink cannot be
// queried. It deliberately omits receiver-specific headers and captions.
func fallbackCaps() (*mediav1.Capabilities, map[mediav1.Container]sinkEntry) {
	const container = mediav1.Container_CONTAINER_MPEGTS
	return &mediav1.Capabilities{
		Provenance: "DLNA compatibility fallback: MPEG-TS, H.264 and stereo AAC (receiver support unverified)",
		Formats: []*mediav1.FormatSupport{{
			Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MPEGTS}},
			Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{
				Video: device.H264Support(),
				Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2},
			}},
		}},
	}, map[mediav1.Container]sinkEntry{container: {mime: device.MIME(container), features: "*"}}
}

// audioSupportFor builds the copy envelope for an audio codec.
func audioSupportFor(codec mediav1.AudioCodec) *mediav1.AudioSupport {
	if codec == mediav1.AudioCodec_AUDIO_CODEC_AAC {
		return &mediav1.AudioSupport{Codec: codec, MaxChannels: 2}
	}
	return &mediav1.AudioSupport{Codec: codec}
}

// parseSinkProtocolInfo maps a ConnectionManager Sink protocolInfo CSV into capabilities and the device's own entry per container.
func parseSinkProtocolInfo(sink string) (*mediav1.Capabilities, map[mediav1.Container]sinkEntry) {
	r := &mediav1.Capabilities{Provenance: "receiver ConnectionManager protocolInfo; video envelope uses conservative family defaults"}
	sinks := map[mediav1.Container]sinkEntry{}
	var captions []mediav1.CaptionFormat
	for entry := range strings.SplitSeq(sink, ",") {
		fields := strings.SplitN(strings.TrimSpace(entry), ":", 4)
		if len(fields) != 4 || !strings.EqualFold(fields[0], "http-get") {
			continue
		}
		mime, pn := strings.ToLower(fields[2]), strings.ToUpper(fields[3])
		switch mime {
		case "application/x-subrip", "text/srt":
			captions = append(captions, mediav1.CaptionFormat_CAPTION_FORMAT_SRT)
		case "text/vtt":
			captions = append(captions, mediav1.CaptionFormat_CAPTION_FORMAT_VTT)
		}
		ct, cok := containerFromMIME(mime)
		video := videoFromProfile(mime, pn)
		a, aok := audioFromProfile(mime, pn)
		if cok && video == nil && mime == device.MIME(ct) && !strings.Contains(pn, "DLNA.ORG_PN=") {
			// A container named by its contract MIME and no profile plays the family default.
			video, a, aok = device.H264Support(), mediav1.AudioCodec_AUDIO_CODEC_AAC, true
		}
		// A separate audio-only profile says nothing about this video container.
		if !cok || video == nil || !aok {
			if cok {
				if _, exists := sinks[ct]; !exists {
					sinks[ct] = sinkEntry{mime: fields[2], features: fields[3]}
				}
			}
			continue
		}
		if h264 := video.GetH264(); h264 != nil && strings.Contains(pn, "_BL_") {
			h264.Profiles = []mediav1.H264Support_Profile{mediav1.H264Support_PROFILE_BASELINE, mediav1.H264Support_PROFILE_CONSTRAINED_BASELINE}
		}
		if h264 := video.GetH264(); h264 != nil && strings.Contains(pn, "_MP_") {
			h264.Profiles = []mediav1.H264Support_Profile{mediav1.H264Support_PROFILE_MAIN}
		}
		if hevc := video.GetHevc(); hevc != nil && strings.Contains(pn, "_MP_") {
			hevc.Profiles = []mediav1.HEVCSupport_Profile{mediav1.HEVCSupport_PROFILE_MAIN}
		}
		progressive := mediav1.ProgressiveSupport_CONTAINER_MP4
		if ct == mediav1.Container_CONTAINER_MPEGTS {
			progressive = mediav1.ProgressiveSupport_CONTAINER_MPEGTS
		}
		format := &mediav1.FormatSupport{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: progressive}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: video, Audio: audioSupportFor(a)}}}
		duplicate := slices.ContainsFunc(r.Formats, func(existing *mediav1.FormatSupport) bool { return proto.Equal(existing, format) })
		alternative := slices.ContainsFunc(r.Formats, func(existing *mediav1.FormatSupport) bool {
			return existing.GetProgressive().GetContainer() == progressive && !proto.Equal(existing, format)
		})
		if !duplicate {
			r.Formats = append(r.Formats, format)
		}
		if alternative || sinks[ct].features == "*" {
			// The play contract names a container, not a DLNA profile. With multiple
			// alternatives, omit the profile rather than advertise one not selected.
			sinks[ct] = sinkEntry{mime: device.MIME(ct), features: "*"}
		} else {
			sinks[ct] = sinkEntry{mime: fields[2], features: fields[3]}
		}
	}
	// Prefer MPEG-TS, then MP4, keeping each receiver-advertised tuple intact.
	slices.SortStableFunc(r.Formats, func(a, b *mediav1.FormatSupport) int {
		return cmp.Compare(b.GetProgressive().GetContainer(), a.GetProgressive().GetContainer())
	})
	for _, f := range r.Formats {
		f.Captions = slices.Clone(captions)
	}
	return r, sinks

}

// videoFromProfile reads the DLNA.ORG_PN token (already upper-cased), failing that the MIME type.
func videoFromProfile(mime, pn string) *mediav1.VideoSupport {
	switch {
	case strings.Contains(pn, "HEVC") || strings.Contains(pn, "H265") || strings.Contains(mime, "hevc") || strings.Contains(mime, "h265"):
		return &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_Hevc{Hevc: &mediav1.HEVCSupport{
			Profiles:  []mediav1.HEVCSupport_Profile{mediav1.HEVCSupport_PROFILE_MAIN, mediav1.HEVCSupport_PROFILE_MAIN_10},
			BitDepths: []uint32{8, 10},
		}}}
	case strings.Contains(pn, "AVC") || strings.Contains(pn, "H264") || strings.Contains(mime, "avc") || strings.Contains(mime, "h264"):
		return device.H264Support()
	}
	return nil
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
	case "video/mp2t", "video/vnd.dlna.mpeg-tts", "video/x-mpegts":
		return mediav1.Container_CONTAINER_MPEGTS, true
	case "video/mp4":
		return mediav1.Container_CONTAINER_MP4, true
	}
	return mediav1.Container_CONTAINER_UNSPECIFIED, false
}
