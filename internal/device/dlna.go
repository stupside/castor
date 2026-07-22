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

	"github.com/stupside/castor/internal/media"
)

// capsTimeout bounds the GetProtocolInfo round-trip: a slow or unresponsive
// ConnectionManager degrades to conservative capabilities instead of stalling
// the cast, since this negotiation now gates the copy-vs-encode decision.
const capsTimeout = 3 * time.Second

// serviceVersions are the UPnP service versions castor looks for, newest
// first. Renderers publish whichever version their firmware implements (Philips
// and Sony sets commonly advertise :3) and service lookup is an exact URN
// match, so asking only for :1 misses them. UPnP requires a higher service
// version to stay backward compatible with the actions of lower ones, and
// castor calls only SetAVTransportURI, Play and GetProtocolInfo — all unchanged
// since v1.
var serviceVersions = []int{3, 2, 1}

// dlnaDevice is a connected UPnP AVTransport renderer. caps is negotiated once
// at connect (see negotiateCaps) and reported verbatim thereafter.
//
// transport is the generic service client rather than a version-specific
// wrapper: the SOAP action namespace must match the service version the device
// published, and goupnp's av1 clients hardcode the :1 URN, which a :3 service
// can reject as an invalid action.
type dlnaDevice struct {
	transport goupnp.ServiceClient
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

	transport, err := findService(loc, u, "AVTransport")
	if err != nil {
		return nil, fmt.Errorf("creating AVTransport client: %w", err)
	}
	return &dlnaDevice{transport: transport, caps: negotiateCaps(ctx, loc, u)}, nil
}

// findService returns a client for the newest version of the named service the
// device publishes, reporting an error when it exposes none castor can drive.
func findService(root *goupnp.RootDevice, loc *url.URL, service string) (goupnp.ServiceClient, error) {
	for _, version := range serviceVersions {
		urn := fmt.Sprintf("urn:schemas-upnp-org:service:%s:%d", service, version)
		clients, err := goupnp.NewServiceClientsFromRootDevice(root, loc, urn)
		if err != nil || len(clients) == 0 {
			continue
		}
		return clients[0], nil
	}
	return goupnp.ServiceClient{}, fmt.Errorf(
		"no %s service (v1-v3) found on device %q (UDN=%q)",
		service, root.Device.FriendlyName, root.Device.UDN)
}

// negotiateCaps asks the renderer what it accepts, over ConnectionManager
// GetProtocolInfo, and maps its advertised Sink into a media.Renderer. It is
// best-effort: any failure, or a renderer that advertises no codec we know,
// degrades to fallbackCaps so playback still works (just conservatively).
func negotiateCaps(ctx context.Context, loc *goupnp.RootDevice, u *url.URL) media.Renderer {
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
		slog.WarnContext(ctx, "renderer advertised no known video codec; using conservative capabilities")
		return fallbackCaps()
	}
	slog.InfoContext(ctx, "negotiated renderer capabilities", "codecs", codecNames(caps.Video), "containers", caps.Containers)
	return caps
}

// codecEnvelope is the codec-fixed part of a stream-copy envelope: the profiles
// and bit depths that black-screen or mis-tone a renderer that can't handle them
// (10-bit H.264 is the rare High 10 profile; HDR needs a TV that engages it).
// Adding a codec the pipeline can encode to is one entry here.
type codecEnvelope struct {
	profiles  []string
	bitDepths []int // nil == 8-bit only
}

var codecEnvelopes = map[media.Codec]codecEnvelope{
	media.CodecH264: {profiles: []string{"Constrained Baseline", "Baseline", "Main", "High"}},
	media.CodecHEVC: {profiles: []string{"Main", "Main 10"}, bitDepths: []int{8, 10}},
}

// videoSupportFor builds the copy envelope for a codec: its decode-safety
// profile and bit-depth constraints.
func videoSupportFor(codec media.Codec) media.VideoSupport {
	env := codecEnvelopes[codec]
	return media.VideoSupport{
		Codec:     codec,
		Profiles:  env.profiles,
		BitDepths: env.bitDepths,
	}
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
		Video:      []media.VideoSupport{videoSupportFor(media.CodecH264)},
	}
}

// parseSinkProtocolInfo maps a ConnectionManager Sink protocolInfo CSV into a
// Renderer. Each entry is "protocol:network:mime:additionalInfo"; the codec is
// read from the DLNA.ORG_PN token (AVC/HEVC) and the MIME, and paired with its
// copy envelope. It is deliberately lenient: the lists renderers return are huge
// and vendor-specific, so anything unrecognised is skipped rather than rejected.
func parseSinkProtocolInfo(sink string) media.Renderer {
	present := map[media.Codec]bool{}
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
		}
		if ct, ok := containerFromMIME(mime); ok {
			containers[ct] = true
		}
	}

	var r media.Renderer
	for _, c := range discoverableCodecs {
		if present[c] {
			r.Video = append(r.Video, videoSupportFor(c))
		}
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

	setURI := &struct {
		InstanceID         string
		CurrentURI         string
		CurrentURIMetaData string
	}{"0", streamURL.String(), metadata}
	if err := d.action(ctx, "SetAVTransportURI", setURI); err != nil {
		return fmt.Errorf("setting transport URI: %w", err)
	}

	play := &struct {
		InstanceID string
		Speed      string
	}{"0", "1"}
	if err := d.action(ctx, "Play", play); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}
	return nil
}

// action performs a SOAP action against the renderer's AVTransport service,
// namespaced to the service version the device actually published. The actions
// routed through here return no out arguments, so no response is unmarshalled.
func (d *dlnaDevice) action(ctx context.Context, name string, request any) error {
	return d.transport.SOAPClient.PerformActionCtx(
		ctx, d.transport.Service.ServiceType, name, request, nil)
}

// transportPollInterval is the cadence WaitDone watches the transport at; each
// GetTransportInfo round-trip is bounded by the same duration so a hung call
// yields to the next poll instead of stalling the watcher.
const transportPollInterval = 2 * time.Second

// transportWatch decides when polled transport states end the cast. A stop is
// only trusted after the renderer has been seen playing: between
// SetAVTransportURI and the first fetched byte, renderers still report
// STOPPED, and that must not end a cast that hasn't started.
type transportWatch struct{ played bool }

// observe feeds one polled CurrentTransportState and reports whether playback
// is over.
func (w *transportWatch) observe(state string) bool {
	switch state {
	case "PLAYING", "PAUSED_PLAYBACK":
		w.played = true
	case "STOPPED", "NO_MEDIA_PRESENT":
		return w.played
	}
	return false
}

// WaitDone polls AVTransport GetTransportInfo until the renderer reports
// playback stopped. SOAP failures are skipped rather than surfaced: a
// momentarily unreachable renderer is usually still playing, and ending the
// cast on a hiccup would cut it off mid-movie.
func (d *dlnaDevice) WaitDone(ctx context.Context) error {
	tick := time.NewTicker(transportPollInterval)
	defer tick.Stop()

	var watch transportWatch
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}

		state, err := d.transportState(ctx)
		if err != nil {
			continue
		}
		if watch.observe(state) {
			return nil
		}
	}
}

// transportState fetches the renderer's CurrentTransportState.
func (d *dlnaDevice) transportState(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, transportPollInterval)
	defer cancel()

	request := &struct{ InstanceID string }{"0"}
	response := &struct {
		CurrentTransportState  string
		CurrentTransportStatus string
		CurrentSpeed           string
	}{}
	if err := d.transport.SOAPClient.PerformActionCtx(
		ctx, d.transport.Service.ServiceType, "GetTransportInfo", request, response); err != nil {
		return "", err
	}
	return response.CurrentTransportState, nil
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
