// Package dlna casts to UPnP MediaRenderers over AVTransport.
package dlna

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"

	"github.com/huin/goupnp"
	"golang.org/x/sync/errgroup"

	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/ssdp"
)

// descriptionReaders bounds the descriptions read at once, however many devices answer.
const descriptionReaders = 16

// serviceVersions: UPnP service versions (newest first); many implement :3, not just :1.
var serviceVersions = []int{3, 2, 1}

// Family is the UPnP AVTransport strategy; its devices do not fetch for themselves.
type Family struct{}

// familyType is the type the contract names this family by.
const familyType device.Type = "dlna"

func (Family) Type() device.Type { return familyType }

var _ device.Family = Family{}

func (Family) Discover(ctx context.Context) []device.Info {
	responses, err := ssdp.Search(ctx, searchTarget, multicastSearchTimeout)
	if err != nil {
		slog.WarnContext(ctx, "dlna discovery error", "error", err)
		return nil
	}
	return readDescriptions(ctx, responses, goupnp.DeviceByURLCtx)
}

// readDescriptions reads the answering devices' descriptions together, so one that never answers hides no other.
func readDescriptions(ctx context.Context, responses []*http.Response, read func(context.Context, *url.URL) (*goupnp.RootDevice, error)) []device.Info {
	found := make([]device.Info, len(responses))
	var readers errgroup.Group
	readers.SetLimit(descriptionReaders)
	for i, response := range responses {
		readers.Go(func() error {
			location, err := response.Location()
			if err != nil || ctx.Err() != nil {
				return nil
			}
			root, err := read(ctx, location)
			if err != nil {
				slog.WarnContext(ctx, "dlna device answered without describing itself", "location", location.String(), "error", err)
				return nil
			}
			found[i] = device.Info{
				ID:      cmp.Or(response.Header.Get("USN"), location.String()),
				Name:    root.Device.FriendlyName,
				Type:    familyType,
				Address: location.String(),
			}
			return nil
		})
	}
	_ = readers.Wait() // no reader reports an error: a device that fails is only left out
	return slices.DeleteFunc(found, func(info device.Info) bool { return info == device.Info{} })
}

// Locate trusts an HTTP address and finds any other with a unicast M-SEARCH, no multicast.
func (Family) Locate(ctx context.Context, address string) (string, error) {
	if u, err := url.Parse(address); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return address, nil
	}

	location, err := searchDescription(ctx, ssdpTarget(address))
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
