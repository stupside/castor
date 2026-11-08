package device

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"connectrpc.com/connect"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/settings"
)

// Discovery supplies device observations; the directory owns identity and cache rules.
type Discovery func(context.Context) ([]Info, error)

// Directory is the devices this server reached, by the id a cast's target names them with.
type Directory struct {
	reach   Registry
	find    Discovery
	resolve func(context.Context, string) ([]net.IPAddr, error)

	mu     sync.Mutex
	seen   discoveries
	leases map[string]*lease
}

// NewDirectory lists devices from find and lends them through reach.
func NewDirectory(reach Registry, find Discovery) *Directory {
	return &Directory{reach: reach, find: find, resolve: net.DefaultResolver.LookupIPAddr, seen: discoveries{}, leases: map[string]*lease{}}
}

func (d *Directory) ListDevices(ctx context.Context, _ *castorv1.ListDevicesRequest) (*castorv1.ListDevicesResponse, error) {
	found, err := d.discover(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("discovering devices: %w", err))
	}
	listed := make([]*castorv1.Device, len(found))
	for i, info := range found {
		listed[i] = info.Public()
	}
	return &castorv1.ListDevicesResponse{Devices: listed}, nil
}

// Target is the device t names: one seen, discovered again when not yet seen, or one pinned at its address.
func (d *Directory) Target(ctx context.Context, t *castorv1.Target) (Info, error) {
	if pinned := t.GetPinned(); pinned != nil {
		kind, err := requestedType(pinned.GetType())
		if err != nil {
			return Info{}, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if _, err := d.reach.family(kind); err != nil {
			return Info{}, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return Info{Type: kind, Address: pinned.GetAddress()}, nil
	}
	if info, ok := d.lookup(t.GetDeviceId()); ok {
		return info, nil
	}
	if _, err := d.discover(ctx); err != nil {
		return Info{}, connect.NewError(connect.CodeUnavailable, fmt.Errorf("discovering devices: %w", err))
	}
	if info, ok := d.lookup(t.GetDeviceId()); ok {
		return info, nil
	}
	return Info{}, connect.NewError(connect.CodeNotFound, fmt.Errorf("no device %q on the network", t.GetDeviceId()))
}

func requestedType(t castorv1.DeviceType) (Type, error) {
	name := settings.EnumName(t)
	if name == "" {
		return "", fmt.Errorf("unknown device type: %v", t)
	}
	return Type(name), nil
}

// Connect opens target for one cast, which holds it to its end; a Play that finds it gone connects it again.
func (d *Directory) Connect(ctx context.Context, target Info) (Device, error) {
	ctx, cancel := context.WithTimeout(ctx, d.reach.Timeout)
	defer cancel()
	family, err := d.reach.family(target.Type)
	if err != nil {
		return nil, err
	}
	resolved, err := locate(ctx, family, target)
	if err != nil {
		return nil, err
	}
	l := newLease(d)
	if err := l.claimResolved(ctx, resolved); err != nil {
		return nil, err
	}
	dev, at, err := d.dial(ctx, resolved)
	if err != nil {
		l.release()
		return nil, err
	}
	if err := l.claimResolved(ctx, at); err != nil {
		_ = dev.Close()
		l.release()
		return nil, err
	}
	r := &reconnecting{dir: d, owner: l}
	r.cur.Store(&link{device: dev, target: at})
	l.device = r
	return l, nil
}

// dial connects target, discovering it again once when a seen device no longer answers where it was found.
func (d *Directory) dial(ctx context.Context, target Info) (Device, Info, error) {
	dev, err := d.reach.Connect(ctx, target)
	if err == nil || target.ID == "" {
		return dev, target, err
	}
	if _, scanErr := d.discover(ctx); scanErr != nil {
		return nil, target, errors.Join(err, fmt.Errorf("rediscovering device: %w", scanErr))
	}
	moved, ok := d.lookup(target.id())
	if !ok || moved.Address == target.Address {
		return nil, target, err
	}
	dev, err = d.reach.Connect(ctx, moved)
	return dev, moved, err
}

func (d *Directory) discover(ctx context.Context) ([]Info, error) {
	ctx, cancel := context.WithTimeout(ctx, d.reach.Timeout)
	defer cancel()
	found, err := d.find(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	listed := make([]Info, 0, len(found))
	for _, info := range found {
		if info.ID == "" || info.Address == "" {
			slog.WarnContext(ctx, "discovery returned an incomplete device", "type", info.Type, "id", info.ID)
			continue
		}
		if _, err := d.reach.family(info.Type); err != nil {
			slog.WarnContext(ctx, "discovery returned an unsupported device", "type", info.Type, "id", info.ID)
			continue
		}
		if !seen[info.id()] {
			seen[info.id()] = true
			listed = append(listed, info)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen.remember(listed)
	return listed, nil
}

func (d *Directory) lookup(id string) (Info, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen.prune(time.Now())
	observed, ok := d.seen[id]
	return observed.info, ok
}
