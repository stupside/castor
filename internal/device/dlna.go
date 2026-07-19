package device

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/dcps/av1"

	"github.com/stupside/castor/internal/media"
)

// dlnaCapabilities describes a Samsung-class DLNA MediaRenderer. Containers are
// the MIME types it plays as-is; the H.264 envelope is what it decodes without
// a re-encode. High 10 / 4:2:2 / 4:4:4 (10-bit, wide chroma) are deliberately
// excluded: they pass a bare "codec == h264" check but the TV black-screens on
// them. Supporting more codecs (HEVC) is a data change here.
var dlnaCapabilities = media.Renderer{
	Containers: []string{"video/mp2t", media.MP4},
	Video: []media.VideoSupport{
		{
			Codec:     media.CodecH264,
			Profiles:  []string{"Constrained Baseline", "Baseline", "Main", "High"},
			MaxLevel:  42,
			MaxHeight: 1080,
		},
	},
}

type dlnaDevice struct {
	transport *av1.AVTransport1
}

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
	return &dlnaDevice{transport: transports[0]}, nil
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
