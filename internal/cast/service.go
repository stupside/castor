package cast

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"slices"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/device/chromecast"
	"github.com/stupside/castor/internal/device/dlna"
	"github.com/stupside/castor/internal/media"
	"github.com/stupside/castor/internal/resolve"
	"github.com/stupside/castor/internal/scraper"
	"github.com/stupside/castor/internal/transcode"
)

// Service orchestrates URL resolution, device discovery, and casting.
type Service struct {
	cfg      *app.Config
	scrapers *scraper.Registry
}

// NewService creates a cast service.
func NewService(cfg *app.Config, scrapers *scraper.Registry) *Service {
	return &Service{cfg: cfg, scrapers: scrapers}
}

// CastURL resolves a direct URL and casts it.
func (s *Service) CastURL(ctx context.Context, itemURL *url.URL) error {
	stream, err := resolve.Resolve(ctx, s.cfg.Resolver.FFprobePath, itemURL)
	if err != nil {
		return fmt.Errorf("resolving URL: %w", err)
	}

	return s.castStream(ctx, stream)
}

// CastMovie extracts a movie stream via a scraper and casts it.
func (s *Service) CastMovie(ctx context.Context, scraperName string, itemID string) error {
	sc, err := s.scrapers.Get(scraperName)
	if err != nil {
		return err
	}

	stream, err := sc.Movie(ctx, s.cfg.Browser, itemID)
	if err != nil {
		return fmt.Errorf("extracting movie stream: %w", err)
	}

	return s.castStream(ctx, stream)
}

// CastEpisode extracts an episode stream via a scraper and casts it.
func (s *Service) CastEpisode(ctx context.Context, scraperName string, itemID string, season, episode uint) error {
	sc, err := s.scrapers.Get(scraperName)
	if err != nil {
		return err
	}

	stream, err := sc.Episode(ctx, s.cfg.Browser, itemID, season, episode)
	if err != nil {
		return fmt.Errorf("extracting episode stream: %w", err)
	}

	return s.castStream(ctx, stream)
}

func (s *Service) castStream(ctx context.Context, stream *media.Stream) error {
	iface, err := net.InterfaceByName(s.cfg.Network.Interface)
	if err != nil {
		return fmt.Errorf("looking up interface %q: %w", s.cfg.Network.Interface, err)
	}

	info, err := device.FindInfo(
		ctx,
		iface, s.cfg.Network.Timeout,
		s.cfg.Device.Type, s.cfg.Device.Name,
	)
	if err != nil {
		return fmt.Errorf("finding device: %w", err)
	}

	var dev device.Device

	switch info.Type {
	case device.TypeDLNA:
		dev = dlna.NewDevice(info, dlna.Config{
			Interface: iface,
		})
	case device.TypeChromecast:
		dev = chromecast.NewDevice(info)
	default:
		return device.UnknownTypeError(info.Type)
	}

	if err := dev.Connect(); err != nil {
		return fmt.Errorf("connecting to device: %w", err)
	}
	defer dev.Close()

	contentType := stream.ContentType
	streamURL := stream.URL

	var srvErr <-chan error

	reqs := dev.Requirements()
	if !slices.Contains(reqs.SupportedContentTypes, contentType) {
		fmtInfo, ok := media.LookupFormat(s.cfg.Transcode.OutputFormat)
		if !ok {
			return fmt.Errorf("unsupported output format %q", s.cfg.Transcode.OutputFormat)
		}

		reader, wait, err := transcode.Transcode(ctx, transcode.Config{
			FFmpegPath: s.cfg.Resolver.FFmpegPath,
			Transcode:  s.cfg.Transcode,
		}, stream.URL)
		if err != nil {
			return fmt.Errorf("starting transcode: %w", err)
		}
		defer reader.Close()
		defer wait()

		localIP, err := localIPFromInterface(iface)
		if err != nil {
			return err
		}

		srv, err := transcode.NewStreamServer(reader, localIP, fmtInfo.ContentType, fmtInfo.Extension)
		if err != nil {
			return fmt.Errorf("starting stream server: %w", err)
		}
		srv.Start()
		defer srv.Stop()

		if err := srv.WaitForData(ctx, 64*1024); err != nil {
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

	if err := dev.Play(ctx, streamURL, contentType); err != nil {
		return err
	}

	// When transcoding, keep ffmpeg + server alive until the device
	// stops consuming or the user cancels.
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

