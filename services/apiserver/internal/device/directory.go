package device

import (
	"context"
	"fmt"
	"sync"

	"connectrpc.com/connect"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// Directory is the devices this server reached, by the id a cast's target names them with.
type Directory struct {
	reach Registry

	mu   sync.Mutex
	seen map[string]Info
}

// NewDirectory lists and lends the devices reach finds.
func NewDirectory(reach Registry) *Directory {
	return &Directory{reach: reach, seen: map[string]Info{}}
}

func (d *Directory) ListDevices(ctx context.Context, _ *castorv1.ListDevicesRequest) (*castorv1.ListDevicesResponse, error) {
	found := d.discover(ctx)
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
	d.discover(ctx)
	if info, ok := d.lookup(t.GetDeviceId()); ok {
		return info, nil
	}
	return Info{}, connect.NewError(connect.CodeNotFound, fmt.Errorf("no device %q on the network", t.GetDeviceId()))
}

func requestedType(t castorv1.DeviceType) (Type, error) {
	for kind, public := range publicTypes {
		if public == t {
			return kind, nil
		}
	}
	return "", fmt.Errorf("unknown device type: %v", t)
}

// Connect opens target for one cast, which holds it to its end; a Play that finds it gone connects it again.
func (d *Directory) Connect(ctx context.Context, target Info) (Device, error) {
	dev, at, err := d.dial(ctx, target)
	if err != nil {
		return nil, err
	}
	return &reconnecting{dir: d, target: at, device: dev}, nil
}

// dial connects target, discovering it again once when a seen device no longer answers where it was found.
func (d *Directory) dial(ctx context.Context, target Info) (Device, Info, error) {
	dev, err := d.reach.Connect(ctx, target)
	if err == nil || target.ID == "" {
		return dev, target, err
	}
	d.discover(ctx)
	moved, ok := d.lookup(target.id())
	if !ok || moved.Address == target.Address {
		return nil, target, err
	}
	dev, err = d.reach.Connect(ctx, moved)
	return dev, moved, err
}

func (d *Directory) discover(ctx context.Context) []Info {
	found := d.reach.Discover(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, info := range found {
		d.seen[info.id()] = info
	}
	return found
}

func (d *Directory) lookup(id string) (Info, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	info, ok := d.seen[id]
	return info, ok
}
