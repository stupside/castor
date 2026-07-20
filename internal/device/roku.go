package device

import (
	"cmp"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huin/goupnp/httpu"
	"github.com/huin/goupnp/ssdp"

	"github.com/stupside/castor/internal/device/rokuchannel"
	"github.com/stupside/castor/internal/media"
)

const (
	rokuSearchTarget   = "roku:ecp"
	rokuDiscoverySends = 3
	rokuDefaultAppID   = "dev"
	rokuDefaultDevUser = "rokudev"
	rokuHTTPTimeout    = 10 * time.Second
)

// RokuOptions carries what discovery can't: which channel to launch and the
// developer-web-server credentials used to sideload Castor's channel.
type RokuOptions struct {
	AppID    string
	Username string
	Password string
}

type rokuDevice struct {
	ecp   *url.URL // http://<ip>:8060
	appID string
	name  string
	hc    *http.Client
}

var _ Device = (*rokuDevice)(nil)

// discoverRoku finds Rokus over SSDP. RawSearch already filters to ST roku:ecp,
// so every response is a Roku; we dedupe re-announcements and resolve names.
func discoverRoku(ctx context.Context) []Info {
	hc, err := httpu.NewHTTPUClient()
	if err != nil {
		slog.WarnContext(ctx, "roku discovery", "error", err)
		return nil
	}
	defer hc.Close()

	responses, err := ssdp.RawSearch(ctx, hc, rokuSearchTarget, rokuDiscoverySends)
	if err != nil {
		slog.WarnContext(ctx, "roku discovery", "error", err)
		return nil
	}

	client := &http.Client{Timeout: rokuHTTPTimeout}
	var devices []Info
	seen := make(map[string]struct{})
	for _, resp := range responses {
		loc, err := resp.Location()
		if err != nil {
			continue
		}
		key := cmp.Or(resp.Header.Get("USN"), loc.String())
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if info, ok := rokuInfo(loc.String(), rokuName(ctx, client, loc)); ok {
			devices = append(devices, info)
		}
	}
	return devices
}

func rokuInfo(location, name string) (Info, bool) {
	u, err := url.Parse(location)
	if err != nil || u.Host == "" {
		return Info{}, false
	}
	return Info{
		Name:    cmp.Or(name, u.Hostname()),
		Type:    TypeRoku,
		Address: location,
	}, true
}

// rokuName reads the owner-set name from /query/device-info, falling back to the
// host on any failure.
func rokuName(ctx context.Context, hc *http.Client, ecpRoot *url.URL) string {
	u := *ecpRoot
	u.Path = "/query/device-info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ecpRoot.Hostname()
	}
	resp, err := hc.Do(req)
	if err != nil {
		return ecpRoot.Hostname()
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return ecpRoot.Hostname()
	}
	return cmp.Or(parseDeviceInfoName(body), ecpRoot.Hostname())
}

func parseDeviceInfoName(body []byte) string {
	var info struct {
		UserDeviceName string `xml:"user-device-name"`
		ModelName      string `xml:"model-name"`
	}
	if err := xml.Unmarshal(body, &info); err != nil {
		return ""
	}
	return cmp.Or(strings.TrimSpace(info.UserDeviceName), strings.TrimSpace(info.ModelName))
}

// connectRoku resolves the ECP base URL and ensures Castor's channel is present.
// ECP is stateless, so there is no connection to hold.
func connectRoku(ctx context.Context, info Info, opts RokuOptions) (Device, error) {
	root, err := url.Parse(info.Address)
	if err != nil || root.Host == "" {
		return nil, fmt.Errorf("parsing roku address %q: %w", info.Address, err)
	}

	dev := &rokuDevice{
		ecp:   &url.URL{Scheme: "http", Host: root.Host},
		appID: cmp.Or(opts.AppID, rokuDefaultAppID),
		name:  info.Name,
		hc:    &http.Client{Timeout: rokuHTTPTimeout},
	}

	// Only the sideloaded dev channel is Castor-managed; a published one is
	// assumed installed.
	if dev.appID == rokuDefaultAppID {
		if err := dev.ensureChannel(ctx, opts); err != nil {
			return nil, err
		}
	}
	return dev, nil
}

func (r *rokuDevice) ensureChannel(ctx context.Context, opts RokuOptions) error {
	installed, err := r.hasDevChannel(ctx)
	if err != nil {
		return fmt.Errorf("querying roku apps: %w", err)
	}
	if installed {
		return nil
	}
	if opts.Password == "" {
		return fmt.Errorf("roku channel not installed and no developer password set: enable Developer Mode on the Roku, set a web-server password, and put it in device.roku.password")
	}
	slog.InfoContext(ctx, "sideloading roku channel", "host", r.ecp.Hostname())
	return r.sideloadChannel(ctx, cmp.Or(opts.Username, rokuDefaultDevUser), opts.Password)
}

func (r *rokuDevice) hasDevChannel(ctx context.Context) (bool, error) {
	u := *r.ecp
	u.Path = "/query/apps"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false, err
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("query/apps: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, err
	}
	return devChannelInstalled(body), nil
}

func devChannelInstalled(body []byte) bool {
	var list struct {
		Apps []struct {
			ID string `xml:"id,attr"`
		} `xml:"app"`
	}
	if err := xml.Unmarshal(body, &list); err != nil {
		return false
	}
	for _, a := range list.Apps {
		if a.ID == rokuDefaultAppID {
			return true
		}
	}
	return false
}

// Play launches the channel pointed at streamURL.
func (r *rokuDevice) Play(ctx context.Context, streamURL *url.URL, contentType string) error {
	q := url.Values{}
	q.Set(rokuchannel.ParamURL, streamURL.String())
	q.Set(rokuchannel.ParamFormat, streamFormatFor(contentType))

	u := *r.ecp
	u.Path = "/launch/" + r.appID
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		return fmt.Errorf("launching roku channel: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("roku launch: %s (channel %q installed?)", resp.Status, r.appID)
	}
	return nil
}

// streamFormatFor maps a content type to a Roku Video streamFormat, defaulting to
// hls (what planRoku produces).
func streamFormatFor(contentType string) string {
	switch contentType {
	case media.MP4:
		return "mp4"
	case media.MKV:
		return "mkv"
	default:
		return "hls"
	}
}

// rokuCapabilities has no video envelope: Roku decodes H.264/HEVC, so video is
// stream-copied and never re-encoded.
var rokuCapabilities = media.Renderer{
	Containers: []string{media.HLS, media.MP4, media.MKV},
}

func (r *rokuDevice) Capabilities() media.Renderer           { return rokuCapabilities }
func (r *rokuDevice) StreamHeaders(string) map[string]string { return nil }
func (r *rokuDevice) Close() error                           { return nil }
