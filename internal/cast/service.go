package cast

import (
	"context"
	"fmt"
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

	var srvErr <-chan error

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
		defer reader.Close()
		defer func() {
			if err := wait(); err != nil {
				slog.WarnContext(ctx, "ffmpeg exited with error", "error", err)
			}
		}()

		localIP, err := localIPFromInterface(iface)
		if err != nil {
			return fmt.Errorf("resolving local IP: %w", err)
		}

		var streamHeaders map[string]string
		if info.Type == device.TypeDLNA {
			streamHeaders = dlna.StreamHeaders(fmtInfo.ContentType)
		}

		srv, err := transcode.NewStreamServer(transcode.StreamServerConfig{
			LocalIP:        localIP,
			ContentType:    fmtInfo.ContentType,
			Extension:      fmtInfo.Extension,
			Headers:        streamHeaders,
			BufferCapacity: cfg.Transcode.BufferCapacity,
			ReadBufSize:    cfg.Transcode.ReadBufferSize,
		})
		if err != nil {
			return fmt.Errorf("starting stream server: %w", err)
		}
		srv.Start(reader)
		defer srv.Stop()

		if err := srv.WaitForData(ctx, cfg.Transcode.InitialDataThreshold); err != nil {
			return fmt.Errorf("waiting for initial stream data: %w", err)
		}

		srvURL, err := srv.URL()
		if err != nil {
			return fmt.Errorf("getting stream server URL: %w", err)
		}

		streamURL = srvURL
		contentType = fmtInfo.ContentType
		srvErr = srv.Err()
	}

	slog.InfoContext(ctx, "starting playback on device", "stream_url", streamURL.String(), "content_type", contentType)

	if err := dev.Play(ctx, streamURL, contentType); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}

	if srvErr != nil {
		select {
		case err := <-srvErr:
			return fmt.Errorf("stream server failed: %w", err)
		case <-ctx.Done():
			return nil
		}
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
