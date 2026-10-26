// Package dlna casts to UPnP MediaRenderers over AVTransport.
package dlna

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/huin/goupnp"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// serviceVersions: UPnP service versions (newest first); many implement :3, not just :1.
var serviceVersions = []int{3, 2, 1}

// Family is the UPnP AVTransport strategy; its devices do not fetch for themselves.
type Family struct{}

// familyType is the type the contract names this family by.
const familyType device.Type = "dlna"

func (Family) Type() device.Type { return familyType }

var _ device.Family = Family{}

func (Family) Discover(ctx context.Context) []device.Info {
	results, err := goupnp.DiscoverDevicesCtx(ctx, searchTarget)
	if err != nil {
		slog.WarnContext(ctx, "dlna discovery error", "error", err)
		return nil
	}

	var devices []device.Info
	for _, result := range results {
		if info, ok := info(result); ok {
			devices = append(devices, info)
		}
	}
	return devices
}

func info(result goupnp.MaybeRootDevice) (device.Info, bool) {
	if result.Root == nil || result.Location == nil {
		return device.Info{}, false
	}

	return device.Info{
		ID:      cmp.Or(result.USN, result.Location.String()),
		Name:    result.Root.Device.FriendlyName,
		Type:    familyType,
		Address: result.Location.String(),
	}, true
}

// Locate trusts an HTTP address and finds any other with a unicast M-SEARCH, no multicast.
func (Family) Locate(ctx context.Context, address string) (string, error) {
	if u, err := url.Parse(address); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return address, nil
	}

	location, err := searchDescription(ctx, address)
	if err != nil {
		return "", fmt.Errorf(
			"resolving DLNA description for %q (device did not answer a unicast SSDP search; "+
				"pin it at its full description URL instead): %w", address, err)
	}
	return location, nil
}

// Connect negotiates capabilities at bind time.
func (Family) Connect(ctx context.Context, info device.Info) (device.Device, error) {
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
	caps, sinks, err := negotiateCaps(ctx, loc, u)
	if err != nil {
		return nil, err
	}
	streamed(caps)
	return &session{transport: transport, caps: caps, sinks: sinks}, nil
}

// findService returns a client for the newest version of the named service the device publishes.
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
