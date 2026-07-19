package device

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/dcps/av1"

	"github.com/stupside/castor/internal/media"
)

// capsTimeout bounds the GetProtocolInfo round-trip: a slow or unresponsive
// ConnectionManager degrades to conservative capabilities instead of stalling
// the cast, since this negotiation now gates the copy-vs-encode decision.
const capsTimeout = 3 * time.Second

// dlnaDevice is a connected UPnP AVTransport renderer. caps is negotiated once
// at connect (see negotiateCaps) and reported verbatim thereafter.
type dlnaDevice struct {
	transport *av1.AVTransport1
	caps      media.Renderer
}

func (d *dlnaDevice) Capabilities() media.Renderer { return d.caps }

var _ Device = (*dlnaDevice)(nil)

// discoverDLNA browses SSDP for UPnP MediaRenderer devices until ctx expires.
func discoverDLNA(ctx context.Context) []Info {
	results, err := goupnp.DiscoverDevicesCtx(ctx, "urn:schemas-upnp-org:device:MediaRenderer:1")
	if err != nil {
		slog.WarnContext(ctx, "dlna discovery error", "error", err)
		return nil
	}

	var devices []Info
	seen := make(map[string]struct{})
	for _, result := range results {
		info, ok := dlnaInfo(result)
		if !ok {
			continue
		}

		// SSDP re-announces the same device; dedupe by USN, falling back
		// to the resolved address when the announcement carries no USN.
		key := cmp.Or(result.USN, info.Address)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		devices = append(devices, info)
	}
	return devices
}

// dlnaInfo maps a discovered UPnP root device to a device Info, reporting false
// when the announcement carries no reachable device to name.
func dlnaInfo(result goupnp.MaybeRootDevice) (Info, bool) {
	if result.Root == nil || result.Location == nil {
		return Info{}, false
	}

	return Info{
		Name:    result.Root.Device.FriendlyName,
		Type:    TypeDLNA,
		Address: result.Location.String(),
	}, true
}

func connectDLNA(ctx context.Context, info Info) (Device, error) {
	u, err := url.Parse(info.Address)
	if err != nil {
		return nil, fmt.Errorf("parsing device location URL: %w", err)
	}

	loc, err := goupnp.DeviceByURLCtx(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("fetching device description: %w", err)
	}

	transports, err := av1.NewAVTransport1ClientsFromRootDevice(loc, u)
	if err != nil {
		return nil, fmt.Errorf("creating AVTransport client: %w", err)
	}
	if len(transports) == 0 {
		return nil, fmt.Errorf("no AVTransport service found on device")
	}
	return &dlnaDevice{transport: transports[0], caps: negotiateCaps(ctx, loc, u)}, nil
}

// negotiateCaps asks the renderer what it accepts, over ConnectionManager
// GetProtocolInfo, and maps its advertised Sink into a media.Renderer. It is
// best-effort: any failure, or a renderer that advertises no codec we know,
// degrades to fallbackCaps so playback still works (just conservatively).
func negotiateCaps(ctx context.Context, loc *goupnp.RootDevice, u *url.URL) media.Renderer {
	managers, err := av1.NewConnectionManager1ClientsFromRootDevice(loc, u)
	if err != nil || len(managers) == 0 {
		slog.WarnContext(ctx, "no ConnectionManager service; using conservative capabilities", "error", err)
		return fallbackCaps()
	}
	ctx, cancel := context.WithTimeout(ctx, capsTimeout)
	defer cancel()
	_, sink, err := managers[0].GetProtocolInfoCtx(ctx)
	if err != nil {
		slog.WarnContext(ctx, "GetProtocolInfo failed; using conservative capabilities", "error", err)
		return fallbackCaps()
	}
	caps := parseSinkProtocolInfo(sink)
	if len(caps.Video) == 0 {
		slog.WarnContext(ctx, "renderer advertised no known video codec; using conservative capabilities")
		return fallbackCaps()
	}
	slog.InfoContext(ctx, "negotiated renderer capabilities", "codecs", codecNames(caps.Video), "containers", caps.Containers)
	return caps
}

// codecEnvelope is the codec-fixed part of a stream-copy envelope: the profiles
// and bit depths that are unsafe to copy regardless of the renderer (10-bit
// H.264 is the rare High 10 profile, and copying it, or any HDR stream, to a TV
// that can't handle it black-screens or mis-tones with no recovery). Resolution
// and level are NOT fixed here; they are filled from what the renderer
// advertises, so a 4K TV copies 4K while an HD-only one stays capped. Adding a
// codec the pipeline can encode to is one entry here.
type codecEnvelope struct {
	profiles  []string
	bitDepths []int // nil == 8-bit only
}

var codecEnvelopes = map[media.Codec]codecEnvelope{
	media.CodecH264: {profiles: []string{"Constrained Baseline", "Baseline", "Main", "High"}},
	media.CodecHEVC: {profiles: []string{"Main", "Main 10"}, bitDepths: []int{8, 10}},
}

// defaultCopyHeight is the resolution ceiling for a codec a renderer advertised
// without a parseable resolution class. We trust the advertised codec up to 4K
// rather than force a re-encode: a renderer that decodes a codec almost always
// decodes it at its own panel resolution, and this keeps 4K passthrough working
// against the vendor-specific tokens that omit a class. Total negotiation
// failure is handled more conservatively, by fallbackCaps.
const defaultCopyHeight = 2160

// videoSupportFor builds the copy envelope for a codec at a discovered maximum
// display height: the codec-fixed profile/bit-depth safety, plus the resolution
// and the level that resolution needs.
func videoSupportFor(codec media.Codec, maxHeight int) media.VideoSupport {
	env := codecEnvelopes[codec]
	return media.VideoSupport{
		Codec:     codec,
		Profiles:  env.profiles,
		BitDepths: env.bitDepths,
		MaxHeight: maxHeight,
		MaxLevel:  maxLevelForHeight(codec, maxHeight),
	}
}

// maxLevelForHeight is the highest codec level a copy of the given height should
// carry (ffprobe reports H.264 level x10, HEVC x30). 4K needs level 5.2 (H.264
// 60fps / HEVC); 1080p fits level 4.2 (H.264) / 4.1 (HEVC).
func maxLevelForHeight(codec media.Codec, height int) int {
	uhd := height > 1080
	switch codec {
	case media.CodecH264:
		if uhd {
			return 52
		}
		return 42
	case media.CodecHEVC:
		if uhd {
			return 156
		}
		return 123
	}
	return 0
}

// resolutionHeight reads a DLNA.ORG_PN token's resolution class (already
// upper-cased): DLNA encodes it as an infix (SD, HD/FHD up to 1080p, UHD/4K at
// 2160p, 8K). 0 means the token carried no class we recognise. UHD is checked
// before HD because "HD" is a substring of "UHD".
func resolutionHeight(pn string) int {
	switch {
	case strings.Contains(pn, "8K") || strings.Contains(pn, "4320"):
		return 4320
	case strings.Contains(pn, "UHD") || strings.Contains(pn, "4K") || strings.Contains(pn, "2160"):
		return 2160
	case strings.Contains(pn, "FHD") || strings.Contains(pn, "HD") || strings.Contains(pn, "1080"):
		return 1080
	}
	return 0
}

// discoverableCodecs is the fixed order capabilities are reported in, so a given
// Sink always yields the same Renderer (and the same is testable).
var discoverableCodecs = []media.Codec{media.CodecH264, media.CodecHEVC}

// fallbackCaps is the conservative envelope used when negotiation yields nothing
// usable: H.264 in MPEG-TS, which every DLNA renderer we target decodes. This is
// the one assumption we keep, and only as a floor.
func fallbackCaps() media.Renderer {
	return media.Renderer{
		Containers: []string{"video/mp2t"},
		Video:      []media.VideoSupport{videoSupportFor(media.CodecH264, 1080)},
	}
}

// parseSinkProtocolInfo maps a ConnectionManager Sink protocolInfo CSV into a
// Renderer. Each entry is "protocol:network:mime:additionalInfo"; the codec is
// read from the DLNA.ORG_PN token (AVC/HEVC) and the MIME, and paired with its
// copy envelope. It is deliberately lenient: the lists renderers return are huge
// and vendor-specific, so anything unrecognised is skipped rather than rejected.
func parseSinkProtocolInfo(sink string) media.Renderer {
	present := map[media.Codec]bool{}
	heights := map[media.Codec]int{} // max advertised display height per codec
	containers := map[string]bool{}
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
			heights[c] = max(heights[c], resolutionHeight(info))
		}
		if ct, ok := containerFromMIME(mime); ok {
			containers[ct] = true
		}
	}

	var r media.Renderer
	for _, c := range discoverableCodecs {
		if !present[c] {
			continue
		}
		height := heights[c]
		if height == 0 { // advertised, but no resolution class in any of its tokens
			height = defaultCopyHeight
		}
		r.Video = append(r.Video, videoSupportFor(c, height))
	}
	for _, ct := range []string{"video/mp2t", media.MP4} {
		if containers[ct] {
			r.Containers = append(r.Containers, ct)
		}
	}
	return r
}

// codecFromProfile identifies the video codec of a Sink entry from its DLNA.ORG_PN
// token (already upper-cased) or, failing that, its MIME type.
func codecFromProfile(mime, pn string) (media.Codec, bool) {
	switch {
	case strings.Contains(pn, "HEVC") || strings.Contains(pn, "H265") || strings.Contains(mime, "hevc") || strings.Contains(mime, "h265"):
		return media.CodecHEVC, true
	case strings.Contains(pn, "AVC") || strings.Contains(pn, "H264") || strings.Contains(mime, "avc") || strings.Contains(mime, "h264"):
		return media.CodecH264, true
	}
	return "", false
}

// containerFromMIME normalises the DLNA MIME spellings we care about to the
// content types the pipeline speaks.
func containerFromMIME(mime string) (string, bool) {
	switch mime {
	case "video/mp2t", "video/mpeg", "video/vnd.dlna.mpeg-tts", "video/x-mpegts":
		return "video/mp2t", true
	case media.MP4:
		return media.MP4, true
	}
	return "", false
}

// codecNames renders a video-support set as its codec names, for logging.
func codecNames(vs []media.VideoSupport) []string {
	names := make([]string, len(vs))
	for i, v := range vs {
		names[i] = string(v.Codec)
	}
	return names
}

// Play sets the AV transport URI and tells the renderer to begin playback.
// The pipeline burns subtitles directly into the video upstream of this call,
// so the renderer plays a single video resource with no separate caption track.
func (d *dlnaDevice) Play(ctx context.Context, streamURL *url.URL, contentType string) error {
	metadata, err := buildDIDLMetadata(streamURL, contentType)
	if err != nil {
		return fmt.Errorf("building DIDL-Lite metadata: %w", err)
	}
	slog.DebugContext(ctx, "DIDL metadata", "xml", metadata)

	if err := d.transport.SetAVTransportURICtx(ctx, 0, streamURL.String(), metadata); err != nil {
		return fmt.Errorf("setting transport URI: %w", err)
	}
	if err := d.transport.PlayCtx(ctx, 0, "1"); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}
	return nil
}

func (d *dlnaDevice) Close() error {
	return nil
}

// StreamHeaders returns the HTTP headers a DLNA renderer expects on a stream
// response. No Content-Length is set: the stream length is unknown and Samsung
// firmwares have been observed to mis-parse very large 64-bit values.
func (d *dlnaDevice) StreamHeaders(contentType string) map[string]string {
	return map[string]string{
		"Connection":               "close",
		"Accept-Ranges":            "none",
		"transferMode.dlna.org":    "Streaming",
		"contentFeatures.dlna.org": contentFeatures(contentType),
	}
}

// DLNA.ORG_FLAGS values (see DLNA Guidelines, Vol. 1, Table 4-129).
// flagsLive: SENDER_PACED+S0_INCREASE+SN_INCREASE+STREAMING+HTTP_STALLING+DLNA_V15.
// flagsFile: STREAMING+HTTP_STALLING+DLNA_V15.
const (
	dlnaFlagsLive = "8D300000000000000000000000000000"
	dlnaFlagsFile = "01300000000000000000000000000000"
)

// dlnaProfileFor returns the DLNA PN and FLAGS for a content type.
// MPEG_TS_HD_NA_ISO is for ffmpeg's 188-byte TS; the bare MPEG_TS_HD_NA
// profile is for 192-byte timestamped packets and Samsung rejects the mismatch.
func dlnaProfileFor(contentType string) (name, flags string) {
	switch contentType {
	case "video/mp2t":
		return "MPEG_TS_HD_NA_ISO", dlnaFlagsLive
	case "video/mp4":
		return "AVC_MP4_HP_HD_AAC", dlnaFlagsFile
	}
	return "", dlnaFlagsLive
}

// contentFeatures returns a DLNA content features string for use in HTTP
// headers and DIDL metadata.
func contentFeatures(contentType string) string {
	name, flags := dlnaProfileFor(contentType)
	return fmt.Sprintf("DLNA.ORG_PN=%s;DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=%s", name, flags)
}
