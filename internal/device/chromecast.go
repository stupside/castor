package device

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/vishen/go-chromecast/application"
	castmedia "github.com/vishen/go-chromecast/cast"
	pb "github.com/vishen/go-chromecast/cast/proto"
	castdns "github.com/vishen/go-chromecast/dns"

	"github.com/stupside/castor/internal/media"
)

const chromecastPort = 8009

type chromecastDevice struct {
	app *application.Application

	// armed gates the media watcher: the receiver also reports the fate of
	// whatever it played before this cast (often IDLE/FINISHED), so only
	// events observed after our own Load may end it.
	armed atomic.Bool
	watch mediaWatch
	once  sync.Once
	done  chan struct{} // closed when the receiver reports playback over
}

var _ Device = (*chromecastDevice)(nil)

// connectChromecast opens the cast application channel. The underlying
// library's Start has no context support, so cancellation cannot interrupt
// the dial. Address may be a bare host (default port 8009) or host:port as
// produced by discovery; cast groups advertise on non-default ports.
func connectChromecast(info Info) (Device, error) {
	host := info.Address
	port := chromecastPort
	if h, p, err := net.SplitHostPort(info.Address); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			host, port = h, n
		}
	}

	app := application.NewApplication(
		application.WithCacheDisabled(true),
	)
	if err := app.Start(host, port); err != nil {
		return nil, fmt.Errorf("connecting to chromecast: %w", err)
	}

	dev := &chromecastDevice{app: app, done: make(chan struct{})}
	app.AddMessageFunc(dev.watchMessage)
	return dev, nil
}

// discoverChromecast browses mDNS (_googlecast._tcp) for cast devices until
// ctx expires. The entries channel is closed by the library when ctx is done,
// so ranging over it consumes the full discovery window.
func discoverChromecast(ctx context.Context) []Info {
	entries, err := castdns.DiscoverCastDNSEntries(ctx, nil)
	if err != nil {
		slog.WarnContext(ctx, "chromecast discovery error", "error", err)
		return nil
	}

	var devices []Info
	seen := make(map[string]struct{})
	for entry := range entries {
		info, ok := chromecastInfo(entry)
		if !ok {
			continue
		}

		// mDNS re-announces the same device; dedupe by UUID, falling back
		// to the resolved address when the entry carries no UUID.
		key := cmp.Or(entry.UUID, info.Address)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		devices = append(devices, info)
	}
	return devices
}

// chromecastInfo maps an mDNS cast entry to a device Info, reporting false when
// the entry advertises no usable IP address. IPv4 is preferred over IPv6, and
// the friendly DeviceName over the mDNS instance and host names. A non-default
// port is preserved as host:port so cast groups, which advertise on random high
// ports rather than 8009, stay connectable; a bare host implies port 8009.
func chromecastInfo(entry castdns.CastEntry) (Info, bool) {
	var host string
	switch {
	case entry.AddrV4 != nil:
		host = entry.AddrV4.String()
	case entry.AddrV6 != nil:
		host = entry.AddrV6.String()
	default:
		return Info{}, false
	}

	address := host
	if entry.Port > 0 && entry.Port != chromecastPort {
		address = net.JoinHostPort(host, strconv.Itoa(entry.Port))
	}

	return Info{
		Name:    cmp.Or(entry.DeviceName, entry.Name, entry.Host),
		Type:    TypeChromecast,
		Address: address,
	}, true
}

func (c *chromecastDevice) Play(_ context.Context, streamURL *url.URL, contentType string) error {
	if err := c.app.Load(streamURL.String(), 0, contentType, false, true, true); err != nil {
		return fmt.Errorf("starting chromecast playback: %w", err)
	}
	c.armed.Store(true)
	return nil
}

// WaitDone blocks until the receiver reports playback over or ctx ends.
func (c *chromecastDevice) WaitDone(ctx context.Context) error {
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// watchMessage feeds receiver events to the media watcher. The library calls
// it from a single dispatch goroutine, so watch needs no locking.
func (c *chromecastDevice) watchMessage(msg *pb.CastMessage) {
	if !c.armed.Load() || msg.GetPayloadUtf8() == "" {
		return
	}
	var resp castmedia.MediaStatusResponse
	if err := json.Unmarshal([]byte(msg.GetPayloadUtf8()), &resp); err != nil {
		return
	}
	if playbackOver(&c.watch, &resp) {
		c.once.Do(func() { close(c.done) })
	}
}

// mediaWatch decides when receiver media statuses end the cast. A terminal
// idle reason only counts after the session has been seen active, so a stale
// IDLE status describing what the receiver played before this cast cannot end
// it before it starts.
type mediaWatch struct{ active bool }

// observe feeds one MEDIA_STATUS entry and reports whether playback is over:
// the media finished, the user cancelled it, another sender interrupted it,
// or the receiver hit an error.
func (w *mediaWatch) observe(playerState, idleReason string) bool {
	switch playerState {
	case "BUFFERING", "PLAYING", "PAUSED":
		w.active = true
	case "IDLE":
		switch idleReason {
		case "FINISHED", "CANCELLED", "INTERRUPTED", "ERROR":
			return w.active
		}
	}
	return false
}

// playbackOver reports whether one receiver message ends the cast: the
// receiver application closing, or a media status the watcher deems terminal.
func playbackOver(w *mediaWatch, resp *castmedia.MediaStatusResponse) bool {
	switch resp.Type {
	case "CLOSE":
		return true
	case "MEDIA_STATUS":
		over := false
		for _, status := range resp.Status {
			over = w.observe(status.PlayerState, status.IdleReason) || over
		}
		return over
	}
	return false
}

func (c *chromecastDevice) Close() error {
	return c.app.Close(false)
}

// chromecastCapabilities: Chromecast decides pass-through purely on the
// container (it never re-encodes video, so it carries no video envelope),
// accepting these MIME types directly.
var chromecastCapabilities = media.Renderer{
	Containers: []string{media.HLS, media.MP4, media.MKV, media.WebM},
}

// Capabilities reports Chromecast's known receiver profile. There is no runtime
// query as there is for DLNA, so this is the documented Cast media support,
// resolved from the device itself for interface parity with the DLNA path.
func (c *chromecastDevice) Capabilities() media.Renderer { return chromecastCapabilities }

// StreamHeaders returns nil: Chromecast needs no protocol-specific headers on
// the local stream server's responses.
func (c *chromecastDevice) StreamHeaders(string) map[string]string {
	return nil
}
