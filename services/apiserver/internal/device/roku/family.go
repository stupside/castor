// Package roku casts to Roku players through a channel castor installs.
package roku

import (
	"cmp"
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/huin/goupnp/httpu"
	"github.com/huin/goupnp/ssdp"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

const (
	searchTarget   = "roku:ecp"
	discoverySends = 3
	devAppID       = "dev"
	devUser        = "rokudev"
	httpTimeout    = 10 * time.Second
	ecpPort        = "8060"
)

// Config is the operator's Roku settings: a published channel to launch, or the developer password to sideload castor's.
type Config struct {
	AppID    string `yaml:"app_id"`
	Password string `yaml:"password"`
}

// Family is the Roku ECP strategy, built with the operator's Roku settings.
type Family struct {
	Config Config
}

// familyType is the type the contract names this family by.
const familyType device.Type = "roku"

func (Family) Type() device.Type { return familyType }

var _ device.Family = Family{}

// Discover finds Rokus over SSDP, naming each once however often it answered.
func (Family) Discover(ctx context.Context) []device.Info {
	hc, err := httpu.NewHTTPUClient()
	if err != nil {
		slog.WarnContext(ctx, "roku discovery", "error", err)
		return nil
	}
	defer hc.Close()

	responses, err := ssdp.RawSearch(ctx, hc, searchTarget, discoverySends)
	if err != nil {
		slog.WarnContext(ctx, "roku discovery", "error", err)
		return nil
	}

	found := map[string]*url.URL{}
	for _, resp := range responses {
		if loc, err := resp.Location(); err == nil && loc.Host != "" {
			found[cmp.Or(resp.Header.Get("USN"), loc.String())] = loc
		}
	}
	ids := slices.Sorted(maps.Keys(found))

	// ONE window for every name, and the lookups run over it together.
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), httpTimeout)
	defer cancel()

	client := &http.Client{Timeout: httpTimeout}
	devices := make([]device.Info, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			loc := found[id]
			devices[i] = device.Info{ID: id, Name: deviceName(nctx, client, loc), Type: familyType, Address: loc.String()}
		})
	}
	wg.Wait()
	return devices
}

// Locate reduces a pinned address to its ECP root, bypassing SSDP discovery.
func (Family) Locate(_ context.Context, address string) (string, error) {
	host := address
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		host = u.Host
	}
	return (&url.URL{Scheme: "http", Host: device.WithPort(host, ecpPort)}).String(), nil
}

// deviceName reads the owner-set name from /query/device-info, falling back to the host on any failure.
func deviceName(ctx context.Context, hc *http.Client, ecpRoot *url.URL) string {
	body, err := get(ctx, hc, ecpRoot.JoinPath("query", "device-info"), 1<<16)
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

func (f Family) Connect(ctx context.Context, info device.Info) (device.Device, error) {
	root, err := url.Parse(info.Address)
	if err != nil || root.Host == "" {
		return nil, fmt.Errorf("parsing roku address %q: %w", info.Address, err)
	}

	dev := &session{
		ecp:   &url.URL{Scheme: "http", Host: root.Host},
		appID: cmp.Or(f.Config.AppID, devAppID),
		name:  info.Name,
		hc:    &http.Client{Timeout: httpTimeout},
	}

	if dev.appID == devAppID {
		if err := dev.ensureChannel(ctx, f.Config); err != nil {
			return nil, err
		}
		return dev, nil
	}

	apps, err := dev.queryApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying roku apps: %w", err)
	}
	if !slices.ContainsFunc(apps, func(a app) bool { return a.ID == dev.appID }) {
		return nil, fmt.Errorf("roku channel %q is not installed on %q", dev.appID, dev.name)
	}
	return dev, nil
}
