package cast

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/device/chromecast"
	"github.com/stupside/castor/internal/device/dlna"
	"github.com/stupside/castor/internal/media"
	"github.com/stupside/castor/internal/resolve"
	"github.com/stupside/castor/internal/transcode"
)

// CastStream resolves and casts a stream, preserving its HTTP headers
// through resolution and transcoding.
func CastStream(ctx context.Context, cfg *app.Config, stream *media.Stream) error {
	resolved, err := resolve.Resolve(ctx, cfg.Resolver, stream)
	if err != nil {
		return fmt.Errorf("resolving URL: %w", err)
	}

	slog.InfoContext(ctx, "stream resolved", "url", resolved.URL.String(), "content_type", resolved.ContentType)

	iface, err := net.InterfaceByName(cfg.Network.Interface)
	if err != nil {
		return fmt.Errorf("looking up interface %q: %w", cfg.Network.Interface, err)
	}

	info, err := device.FindInfo(
		ctx,
		cfg.Network.Timeout,
		cfg.Device.Type, cfg.Device.Name,
	)
	if err != nil {
		return fmt.Errorf("finding device: %w", err)
	}

	slog.InfoContext(ctx, "device found on network", "name", info.Name, "type", string(info.Type), "address", info.Address)

	var dev device.Device

	switch info.Type {
	case device.TypeDLNA:
		dev = dlna.NewDevice(info)
	case device.TypeChromecast:
		dev = chromecast.NewDevice(info)
	default:
		return fmt.Errorf("unknown device type: %q", info.Type)
	}

	if err := dev.Connect(); err != nil {
		return fmt.Errorf("connecting to device: %w", err)
	}
	defer dev.Close()

	slog.InfoContext(ctx, "connected to device", "name", info.Name, "type", string(info.Type))

	contentType := resolved.ContentType
	streamURL := resolved.URL

	var srv *transcode.StreamServer

	if !slices.Contains(dev.SupportedContentTypes(), contentType) {
		slog.InfoContext(ctx, "device does not support content type, transcoding", "content_type", contentType, "output_format", cfg.Transcode.OutputFormat)
		fmtInfo, ok := media.LookupFormat(cfg.Transcode.OutputFormat)
		if !ok {
			return fmt.Errorf("unsupported output format %q", cfg.Transcode.OutputFormat)
		}

		reader, wait, err := transcode.Transcode(ctx, cfg.Transcode, resolved.URL, resolved.Headers)
		if err != nil {
			return fmt.Errorf("starting transcode: %w", err)
		}
		defer func() {
			if err := wait(); err != nil {
				slog.WarnContext(ctx, "ffmpeg exited with error", "error", err)
			}
		}()
		defer reader.Close()

		localIP, err := localIPFromInterface(iface)
		if err != nil {
			return fmt.Errorf("resolving local IP: %w", err)
		}

		var streamHeaders map[string]string
		if info.Type == device.TypeDLNA {
			streamHeaders = dlna.StreamHeaders(fmtInfo.ContentType)
		}

		initial := make([]byte, cfg.Transcode.InitialDataThreshold)
		n, err := io.ReadFull(reader, initial)
		if err != nil && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("waiting for initial transcode data: %w", err)
		}

		srv, err = transcode.NewStreamServer(transcode.StreamServerConfig{
			LocalIP:     localIP,
			ContentType: fmtInfo.ContentType,
			Extension:   fmtInfo.Extension,
			Headers:     streamHeaders,
		}, io.MultiReader(bytes.NewReader(initial[:n]), reader))
		if err != nil {
			return fmt.Errorf("starting stream server: %w", err)
		}
		defer srv.Close()

		streamURL = srv.URL()
		contentType = fmtInfo.ContentType
	}

	slog.InfoContext(ctx, "starting playback on device", "stream_url", streamURL.String(), "content_type", contentType)

	if err := dev.Play(ctx, streamURL, contentType); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}

	if srv != nil {
		return srv.Wait(ctx)
	}

	return nil
}

func localIPFromInterface(iface *net.Interface) (string, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("listing addresses: %w", err)
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			if ip := ipNet.IP.To4(); ip != nil && !ip.IsLoopback() {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no IPv4 address on %s", iface.Name)
}
