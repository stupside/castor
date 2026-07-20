// Package cast turns a resolved media stream into playback on a renderer.
//
// The package is split planner/executor: BuildPlan is the only place that
// branches on device type or source media properties, and the pipeline
// (pipeline.go) executes the plan as a composition of stages — source
// (source.go: pull → spool, gated), subtitles (subtitle.go: whisper + live
// cue writer), and delivery (deliver.go: encode → replay server → device).
package cast

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
	"github.com/stupside/castor/internal/source/resolve"
)

// Play resolves a stream and casts it to the configured device.
//
// The DLNA path reads the upstream URL exactly once: a single puller ffmpeg
// remuxes the source into an on-disk spool — paced like a buffering player
// (initial burst, then 2x realtime) so the CDN doesn't flag it as a ripper
// — and tees a PCM feed to whisper. The encoder reads the growing spool through a
// blocking tail, burns the live transcript into the frames via drawtext,
// and the paced HTTP broadcaster serves the result to the TV. One upstream
// connection means CDN token/rate limits can't kill playback, and once the
// spool is complete the cast is fully offline.
func Play(ctx context.Context, cfg Config, stream *media.Stream) error {
	resolved, localIP, err := resolveSource(ctx, cfg, stream)
	if err != nil {
		return err
	}

	// DLNA always takes the read-once spool path, and planDLNA needs no live
	// device capabilities to decide anything — so we build the plan up front and
	// start pulling immediately, discovering and connecting the renderer
	// concurrently inside the pipeline. Source URLs here are short-lived signed
	// links; keeping SSDP discovery and device connect off the path between
	// capture and the first byte stops the token expiring before we use it.
	if cfg.Device.Type == device.TypeDLNA {
		plan := BuildPlan(PlanInput{
			DeviceType:        device.TypeDLNA,
			SourceURL:         resolved.URL,
			SourceHeaders:     resolved.Headers,
			SourceContentType: resolved.ContentType,
			MaxHeight:         cfg.Resolver.MaxHeight,
			HasSubtitles:      cfg.Whisper.Enable,
		})
		logPlan(ctx, plan)
		return runSpooled(ctx, cfg, plan, localIP, discoverAndConnect(cfg))
	}

	// Every other device type needs the renderer's supported content types to
	// plan (pass-through vs. re-mux), so discover and connect before planning.
	dev, err := discoverAndConnect(cfg)(ctx)
	if err != nil {
		return err
	}
	defer dev.Close()

	plan := BuildPlan(PlanInput{
		DeviceType:        cfg.Device.Type,
		Renderer:          dev.Capabilities(),
		SourceURL:         resolved.URL,
		SourceHeaders:     resolved.Headers,
		SourceContentType: resolved.ContentType,
		MaxHeight:         cfg.Resolver.MaxHeight,
		HasSubtitles:      cfg.Whisper.Enable,
	})
	logPlan(ctx, plan)

	if plan.Transcode == nil {
		slog.InfoContext(ctx, "starting playback", "url", plan.SourceURL.String(), "content_type", plan.OutputContentType)
		if err := dev.Play(ctx, plan.SourceURL, plan.OutputContentType); err != nil {
			return fmt.Errorf("starting playback: %w", err)
		}
		slog.InfoContext(ctx, "playback handed off to device")
		return nil
	}
	return runDirect(ctx, cfg, plan, dev, localIP)
}

func logPlan(ctx context.Context, plan Plan) {
	slog.InfoContext(ctx, "execution plan",
		"transcode", plan.Transcode != nil,
		"spool", plan.Spool,
		"output_content_type", plan.OutputContentType,
		"subtitle_delivery", plan.SubtitleDelivery,
		"send_rate_bps", plan.SendRate,
	)
}

// resolveSource runs the do-or-die prelude that doesn't depend on the renderer:
// resolve the source URL (HLS variant selection) and find our local IPv4. The
// device is discovered separately so its latency can overlap the puller.
func resolveSource(ctx context.Context, cfg Config, stream *media.Stream) (*media.Stream, string, error) {
	slog.InfoContext(ctx, "resolving stream", "url", stream.URL.String())
	resolved, err := resolve.Resolve(ctx, cfg.Resolver, stream)
	if err != nil {
		return nil, "", fmt.Errorf("resolving URL: %w", err)
	}
	slog.InfoContext(ctx, "stream resolved", "url", resolved.URL.String(), "content_type", resolved.ContentType)

	localIP, err := localIPv4(cfg.Network.Interface)
	if err != nil {
		return nil, "", fmt.Errorf("resolving local IP: %w", err)
	}
	return resolved, localIP, nil
}

// deviceConnector discovers and connects the renderer. It's injected so the
// spooled pipeline can run discovery concurrently with the puller (the renderer
// isn't needed until the playback gate opens) and so tests can supply a fake.
type deviceConnector func(ctx context.Context) (device.Device, error)

func discoverAndConnect(cfg Config) deviceConnector {
	return func(ctx context.Context) (device.Device, error) {
		slog.InfoContext(ctx, "discovering device", "name", cfg.Device.Name, "type", string(cfg.Device.Type))
		info, err := device.FindInfo(ctx, cfg.Network.Timeout, cfg.Device.Type, cfg.Device.Name)
		if err != nil {
			return nil, fmt.Errorf("finding device: %w", err)
		}
		slog.InfoContext(ctx, "device found", "name", info.Name, "type", string(info.Type), "address", info.Address)

		dev, err := device.Connect(ctx, info, device.Options{
			Roku: device.RokuOptions{
				AppID:    cfg.Device.Roku.AppID,
				Username: cfg.Device.Roku.Username,
				Password: cfg.Device.Roku.Password,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("connecting to device: %w", err)
		}
		slog.InfoContext(ctx, "connected to device", "name", info.Name)
		return dev, nil
	}
}

// localIPv4 returns the IPv4 address the local stream server should bind:
// the named interface's address, or, when name is empty, the source address
// of the default route. The UDP "connect" performs route selection only —
// no packet is sent.
func localIPv4(ifaceName string) (string, error) {
	if ifaceName == "" {
		conn, err := net.Dial("udp4", "8.8.8.8:53")
		if err != nil {
			return "", fmt.Errorf("detecting default-route address (set network.interface to pin one): %w", err)
		}
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
	}

	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", fmt.Errorf("looking up interface %q: %w", ifaceName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("listing addresses on %s: %w", iface.Name, err)
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if ip := ipNet.IP.To4(); ip != nil && !ip.IsLoopback() {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address on %s", iface.Name)
}
