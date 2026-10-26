package device

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"time"
)

// Registry connects to devices through their protocol families.
type Registry struct {
	Families []Family
	Timeout  time.Duration
}

func (r Registry) family(t Type) (Family, error) {
	for _, f := range r.Families {
		if f.Type() == t {
			return f, nil
		}
	}
	return nil, fmt.Errorf("unknown device type: %q", t)
}

// Connect opens the device at target's address, as its family locates it.
func (r Registry) Connect(ctx context.Context, target Info) (Device, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	f, err := r.family(target.Type)
	if err != nil {
		return nil, err
	}
	info, err := locate(ctx, f, target)
	if err != nil {
		return nil, err
	}
	dev, err := f.Connect(ctx, info)
	if err != nil {
		return nil, fmt.Errorf("connecting to device: %w", err)
	}
	slog.InfoContext(ctx, "connected to device", "name", info.Name, "type", string(info.Type), "address", info.Address)
	return dev, nil
}

// locate resolves target's address into the one its family dials, naming it by its host when it has no name.
func locate(ctx context.Context, f Family, target Info) (Info, error) {
	address, err := f.Locate(ctx, target.Address)
	if err != nil {
		return Info{}, fmt.Errorf("locating device: %w", err)
	}
	target.Name, target.Address = cmp.Or(target.Name, hostLabel(target.Address)), address
	return target, nil
}

// hostLabel names a device pinned at address with no name of its own.
func hostLabel(address string) string {
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		address = u.Host
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		return host
	}
	return address
}
