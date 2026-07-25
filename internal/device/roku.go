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

// RokuConfig carries what discovery can't: which channel to launch (AppID, the
// sideloaded "dev" channel by default) and the developer-web-server Password used
// to sideload Castor's channel the first time. The dev web server's user is
// always "rokudev", so it is not configurable.
type RokuConfig struct {
	AppID    string `yaml:"app_id"`
	Password string `yaml:"password"`
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
// host on any failure. It runs on a fresh timeout derived with the discovery
// context's deadline stripped: ssdp.RawSearch blocks the whole discovery window
// before returning, so the passed ctx is already at its deadline here, and a
// request made on it would fail instantly and every Roku would fall back to its
// IP. WithoutCancel keeps ctx's values but drops that spent deadline.
func rokuName(ctx context.Context, hc *http.Client, ecpRoot *url.URL) string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rokuHTTPTimeout)
	defer cancel()

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

// connectRoku resolves the ECP base URL and makes sure the channel it will launch
// actually exists. ECP is stateless, so there is no connection to hold. The
// sideloaded dev channel is Castor-managed (installed on demand); a published
// app_id is only verified present, so a missing/mistyped id fails here with a
// clear message instead of Play() no-oping on Roku's permissive /launch.
func connectRoku(ctx context.Context, info Info, cfg RokuConfig) (Device, error) {
	root, err := url.Parse(info.Address)
	if err != nil || root.Host == "" {
		return nil, fmt.Errorf("parsing roku address %q: %w", info.Address, err)
	}

	dev := &rokuDevice{
		ecp:   &url.URL{Scheme: "http", Host: root.Host},
		appID: cmp.Or(cfg.AppID, rokuDefaultAppID),
		name:  info.Name,
		hc:    &http.Client{Timeout: rokuHTTPTimeout},
	}

	if dev.appID == rokuDefaultAppID {
		if err := dev.ensureChannel(ctx, cfg); err != nil {
			return nil, err
		}
		return dev, nil
	}

	apps, err := dev.queryApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying roku apps: %w", err)
	}
	if !appInstalled(apps, dev.appID) {
		return nil, fmt.Errorf("roku channel %q is not installed on %q", dev.appID, dev.name)
	}
	return dev, nil
}

// ensureChannel guarantees Castor's own dev channel occupies the sideload slot.
// A Roku holds exactly one dev channel (id "dev"), so a foreign one must be
// replaced, not trusted: devChannelInstalled matches on title as well as id, so a
// non-Castor dev channel does not read as installed and triggers a re-sideload.
func (r *rokuDevice) ensureChannel(ctx context.Context, cfg RokuConfig) error {
	apps, err := r.queryApps(ctx)
	if err != nil {
		return fmt.Errorf("querying roku apps: %w", err)
	}
	if devChannelInstalled(apps) {
		return nil
	}
	if cfg.Password == "" {
		if appInstalled(apps, rokuDefaultAppID) {
			return fmt.Errorf("a different sideloaded channel occupies the Roku dev slot; set device.roku.password so Castor can replace it")
		}
		return fmt.Errorf("roku channel not installed and no developer password set: enable Developer Mode on the Roku, set a web-server password, and put it in device.roku.password")
	}
	slog.InfoContext(ctx, "sideloading roku channel", "host", r.ecp.Hostname())
	return r.sideloadChannel(ctx, rokuDefaultDevUser, cfg.Password)
}

// queryApps fetches the /query/apps listing.
func (r *rokuDevice) queryApps(ctx context.Context) ([]byte, error) {
	u := *r.ecp
	u.Path = "/query/apps"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query/apps: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

type rokuApp struct {
	ID    string `xml:"id,attr"`
	Title string `xml:",chardata"`
}

func parseApps(body []byte) []rokuApp {
	var list struct {
		Apps []rokuApp `xml:"app"`
	}
	if err := xml.Unmarshal(body, &list); err != nil {
		return nil
	}
	return list.Apps
}

// devChannelInstalled reports whether Castor's own dev channel is present: the
// dev slot is occupied AND its title is Castor's, so a foreign sideloaded channel
// is not mistaken for ours (Roku allows only one dev channel at a time).
func devChannelInstalled(body []byte) bool {
	for _, a := range parseApps(body) {
		if a.ID == rokuDefaultAppID && strings.TrimSpace(a.Title) == rokuchannel.Title {
			return true
		}
	}
	return false
}

// appInstalled reports whether an app with the given id is installed.
func appInstalled(body []byte, id string) bool {
	for _, a := range parseApps(body) {
		if a.ID == id {
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
// hls (what the served remux produces).
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

// rokuCapabilities describes what Castor's channel plays. Roku self-fetches the
// URL it is handed (its Video node pulls the stream over the network), so an
// accepted source container casts straight through; anything else is served as a
// live remux (see ServedContainer, set to HLS: Roku has no supported way to play
// a growing single-file URL). It carries no video envelope on purpose: the served
// path stream-copies video unconditionally (Roku decodes H.264/HEVC), so no
// copy-vs-encode gate reads one. The audio envelope lets a 5.1/7.1 AC-3/E-AC-3 or
// AAC track pass the remux through intact instead of being downmixed to stereo.
var rokuCapabilities = media.Renderer{
	SelfFetch:       true,
	Containers:      []string{media.HLS, media.MP4, media.MKV},
	ServedContainer: media.HLS,
	Audio: []media.AudioSupport{
		{Codec: media.CodecAAC, MaxChannels: 6},
		{Codec: media.CodecAC3},
		{Codec: media.CodecEAC3},
	},
}

func (r *rokuDevice) Capabilities() media.Renderer           { return rokuCapabilities }
func (r *rokuDevice) StreamHeaders(string) map[string]string { return nil }
func (r *rokuDevice) Close() error                           { return nil }
