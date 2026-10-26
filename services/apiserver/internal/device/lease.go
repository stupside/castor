package device

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/sync/semaphore"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

// Busy means another cast owns this receiver. Stop that cast before starting
// another; implicit replacement would otherwise let old cleanup stop new media.
type Busy struct{ Address string }

func (e *Busy) Error() string {
	return fmt.Sprintf("device %q is busy; stop its current cast before starting another", e.Address)
}

// lease retains ownership until all commands and final STOP/Close have finished.
// Endpoint and discovery ID are aliases, so pinning a discovered device cannot
// bypass ownership. Explicit nondefault ports identify separate endpoints.
type lease struct {
	dir       *Directory
	device    Device
	commands  *semaphore.Weighted
	lifetime  context.Context
	shutdown  func() error
	mu        sync.Mutex
	observers sync.WaitGroup
}

var errLeaseClosed = errors.New("device lease is closed")

func newLease(dir *Directory) *lease {
	lifetime, cancel := context.WithCancelCause(context.Background())
	l := &lease{dir: dir, commands: semaphore.NewWeighted(1), lifetime: lifetime}
	l.shutdown = sync.OnceValue(func() error {
		l.mu.Lock()
		cancel(errLeaseClosed)
		l.mu.Unlock()
		// Cancellation stops both queued and executing commands. Ownership stays
		// held until those commands return and final device cleanup has completed.
		_ = l.commands.Acquire(context.Background(), 1)
		defer l.commands.Release(1)
		l.observers.Wait()
		err := l.device.Close()
		l.release()
		return err
	})
	return l
}

func endpoint(info Info) string {
	address := info.Address
	port := map[Type]string{"chromecast": "8009", "roku": "8060", "dlna": "80"}[info.Type]
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		address = u.Host
		if u.Scheme == "https" {
			port = "443"
		}
	}
	host, explicit, err := net.SplitHostPort(address)
	if err == nil {
		address, port = host, explicit
	}
	host = strings.Trim(address, "[]")
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	}
	return string(info.Type) + ":endpoint:" + net.JoinHostPort(strings.ToLower(host), port)
}

func (l *lease) claimResolved(ctx context.Context, info Info) error {
	keys := []string{endpoint(info)}
	host, port, err := net.SplitHostPort(strings.TrimPrefix(keys[0], string(info.Type)+":endpoint:"))
	if err != nil {
		return err
	}
	if _, err := netip.ParseAddr(host); err != nil {
		addresses, err := l.dir.resolve(ctx, host)
		if err != nil {
			return fmt.Errorf("resolving device identity %q: %w", host, err)
		}
		if len(addresses) == 0 {
			return fmt.Errorf("device identity %q resolves to no address", host)
		}
		for _, address := range addresses {
			alias := info
			alias.Address = net.JoinHostPort(address.String(), port)
			keys = append(keys, endpoint(alias))
		}
	}
	return l.claimKeys(info, keys)
}
func (l *lease) claimKeys(info Info, keys []string) error {
	if info.ID != "" {
		keys = append(keys, "id:"+info.id())
	}
	l.dir.mu.Lock()
	defer l.dir.mu.Unlock()
	// Known discovery identities survive pinning and later address changes.
	for _, observed := range l.dir.seen {
		known := observed.info
		for _, key := range keys {
			if key == endpoint(known) && known.ID != "" {
				keys = append(keys, "id:"+known.id())
				break
			}
		}
	}
	for _, key := range keys {
		if owner := l.dir.leases[key]; owner != nil && owner != l {
			return &Busy{Address: info.Address}
		}
	}
	for _, key := range keys {
		if l.dir.leases[key] != l {
			l.dir.leases[key] = l
		}
	}
	return nil
}
func (l *lease) release() {
	l.dir.mu.Lock()
	defer l.dir.mu.Unlock()
	for key, owner := range l.dir.leases {
		if owner == l {
			delete(l.dir.leases, key)
		}
	}
}
func (l *lease) command(ctx context.Context, fn func(context.Context, Device) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := context.AfterFunc(l.lifetime, func() { cancel(context.Cause(l.lifetime)) })
	defer stop()
	if err := context.Cause(l.lifetime); err != nil {
		return err
	}
	if err := l.commands.Acquire(ctx, 1); err != nil {
		return context.Cause(ctx)
	}
	defer l.commands.Release(1)
	if err := context.Cause(l.lifetime); err != nil {
		return err
	}
	return fn(ctx, l.device)
}
func (l *lease) Play(ctx context.Context, playback Playback) error {
	return l.command(ctx, func(ctx context.Context, d Device) error { return d.Play(ctx, playback) })
}
func (l *lease) Stop(ctx context.Context) error {
	return l.command(ctx, func(ctx context.Context, d Device) error { return d.Stop(ctx) })
}
func (l *lease) Pause(ctx context.Context) error {
	return l.command(ctx, func(ctx context.Context, d Device) error { return d.Pause(ctx) })
}
func (l *lease) Resume(ctx context.Context) error {
	return l.command(ctx, func(ctx context.Context, d Device) error { return d.Resume(ctx) })
}
func (l *lease) Position(ctx context.Context) (head Playhead, err error) {
	err = l.observe(ctx, func(ctx context.Context) (err error) {
		head, err = l.device.Position(ctx)
		return err
	})
	return head, err
}
func (l *lease) Capabilities() *mediav1.Capabilities { return l.device.Capabilities() }

func (l *lease) AwaitEnd(ctx context.Context) error { return l.observe(ctx, l.device.AwaitEnd) }

// observe runs fn outside the command slot while the lease lives; Close cancels and waits for it.
func (l *lease) observe(ctx context.Context, fn func(context.Context) error) error {
	l.mu.Lock()
	if err := context.Cause(l.lifetime); err != nil {
		l.mu.Unlock()
		return err
	}
	l.observers.Add(1)
	l.mu.Unlock()
	defer l.observers.Done()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := context.AfterFunc(l.lifetime, func() { cancel(context.Cause(l.lifetime)) })
	defer stop()
	return fn(ctx)
}
func (l *lease) Close() error { return l.shutdown() }
