package device

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"sync"
	"time"
)

// Registry is every family this process reaches devices through, and how long each step is given.
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

// Discover sweeps every family at once, each device once however often it announced itself.
func (r Registry) Discover(ctx context.Context) []Info {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	found := make([][]Info, len(r.Families))
	var wg sync.WaitGroup
	for i, f := range r.Families {
		wg.Go(func() { found[i] = f.Discover(ctx) })
	}
	wg.Wait()
	seen := map[Info]bool{}
	return slices.DeleteFunc(slices.Concat(found...), func(info Info) bool {
		key := Info{Type: info.Type, ID: info.ID}
		dup := seen[key]
		seen[key] = true
		return dup
	})
}

// Connect opens the device at target's address, as its family locates it.
func (r Registry) Connect(ctx context.Context, target Info) (Device, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	f, err := r.family(target.Type)
	if err != nil {
		return nil, err
	}
	info, err := locate(ctx, f, target, r.Timeout)
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
func locate(ctx context.Context, f Family, target Info, timeout time.Duration) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	address, err := f.Locate(ctx, target.Address)
	if err != nil {
		return Info{}, fmt.Errorf("locating device: %w", err)
	}
	return Info{ID: target.ID, Name: cmp.Or(target.Name, hostLabel(target.Address)), Type: target.Type, Address: address}, nil
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
