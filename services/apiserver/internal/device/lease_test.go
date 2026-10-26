package device

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type leaseFamily struct {
	stub
	connects atomic.Int32
	stops    atomic.Int32
	fail     atomic.Bool
}

func (f *leaseFamily) Connect(ctx context.Context, _ Info) (Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.fail.Load() {
		return nil, errors.New("dial failed")
	}
	f.connects.Add(1)
	return &leaseDevice{family: f}, nil
}

type leaseDevice struct {
	idle
	family *leaseFamily
}

func (d *leaseDevice) Stop(context.Context) error { d.family.stops.Add(1); return nil }

func TestLeaseAliasesAndStaleClose(t *testing.T) {
	f := &leaseFamily{}
	d := directoryWithFamilies(Registry{Families: []Family{f}, Timeout: time.Second})
	target := Info{Type: "push", ID: "receiver", Address: "http://192.0.2.1:1234/desc"}
	first, err := d.Connect(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []Info{
		{Type: "push", Address: "192.0.2.1:1234"},
		{Type: "push", ID: "receiver", Address: "192.0.2.2:1234"},
	} {
		if _, err := d.Connect(t.Context(), alias); err == nil {
			t.Fatal("overlapping device ownership accepted")
		} else if _, ok := errors.AsType[*Busy](err); !ok {
			t.Fatal(err)
		}
	}
	if f.connects.Load() != 1 {
		t.Fatal("busy cast connected to receiver")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := d.Connect(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Stop(t.Context()); err == nil {
		t.Fatal("closed lease sent STOP")
	}
	if f.stops.Load() != 1 {
		t.Fatal("stale cleanup stopped new playback")
	}
	_ = second.Close()
}
func TestFailedAndCanceledConnectReleaseLease(t *testing.T) {
	f := &leaseFamily{}
	d := directoryWithFamilies(Registry{Families: []Family{f}, Timeout: time.Second})
	target := Info{Type: "push", Address: "192.0.2.1"}
	f.fail.Store(true)
	if _, err := d.Connect(t.Context(), target); err == nil {
		t.Fatal("expected failure")
	}
	f.fail.Store(false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.Connect(ctx, target); err == nil {
		t.Fatal("expected cancellation")
	}
	dev, err := d.Connect(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	_ = dev.Close()
	if len(d.leases) != 0 {
		t.Fatal("leases retained after cleanup")
	}
}
func TestConcurrentLeaseAdmission(t *testing.T) {
	f := &leaseFamily{}
	d := directoryWithFamilies(Registry{Families: []Family{f}, Timeout: time.Second})
	var wg sync.WaitGroup
	admitted := make(chan Device, 64)
	for range 64 {
		wg.Go(func() {
			dev, err := d.Connect(t.Context(), Info{Type: "push", Address: "192.0.2.1"})
			if err == nil {
				admitted <- dev
			}
		})
	}
	wg.Wait()
	close(admitted)
	if len(admitted) != 1 {
		t.Fatalf("admitted %d owners", len(admitted))
	}
	for dev := range admitted {
		_ = dev.Close()
	}
}
func TestCanonicalEndpoints(t *testing.T) {
	for _, pair := range [][2]string{{"10.0.0.1", "http://10.0.0.1:8060/"}, {"[fe80::1%en0]:8060", "http://[fe80::1%25en0]:8060/query"}} {
		if endpoint(Info{Type: "roku", Address: pair[0]}) != endpoint(Info{Type: "roku", Address: pair[1]}) {
			t.Errorf("aliases differ: %v", pair)
		}
	}
}

type closingDevice struct {
	idle
	started chan struct{}
	finish  chan struct{}
}

func (d *closingDevice) Close() error {
	close(d.started)
	<-d.finish
	return errors.New("close failed")
}
func TestLeaseHeldUntilFailedCleanupFinishes(t *testing.T) {
	d := directoryWithFamilies(Registry{})
	target := Info{Type: "push", Address: "192.0.2.1"}
	dev := &closingDevice{started: make(chan struct{}), finish: make(chan struct{})}
	l := newLease(d)
	l.device = dev
	if err := l.claimKeys(target, []string{endpoint(target)}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- l.Close() }()
	<-dev.started
	next := newLease(d)
	if err := next.claimKeys(target, []string{endpoint(target)}); err == nil {
		t.Fatal("lease released before cleanup finished")
	}
	close(dev.finish)
	if err := <-done; err == nil {
		t.Fatal("close error hidden")
	}
	if err := next.claimKeys(target, []string{endpoint(target)}); err != nil {
		t.Fatal(err)
	}
	next.release()
}

func TestHostnameAndIPShareLease(t *testing.T) {
	f := &leaseFamily{}
	d := directoryWithFamilies(Registry{Families: []Family{f}, Timeout: time.Second})
	d.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
	}
	first, err := d.Connect(t.Context(), Info{Type: "push", Address: "receiver.local:1234"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := d.Connect(t.Context(), Info{Type: "push", Address: "192.0.2.1:1234"}); err == nil {
		t.Fatal("DNS alias bypassed ownership")
	}
}
func TestUnresolvableIdentityCannotBypassLease(t *testing.T) {
	f := &leaseFamily{}
	d := directoryWithFamilies(Registry{Families: []Family{f}, Timeout: time.Second})
	d.resolve = func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("no DNS answer") }
	if _, err := d.Connect(t.Context(), Info{Type: "push", Address: "receiver.local:1234"}); err == nil {
		t.Fatal("unresolved alias admitted")
	}
	if f.connects.Load() != 0 {
		t.Fatal("unidentified device was connected")
	}
}

func TestPinnedKnownIdentitySurvivesAddressChange(t *testing.T) {
	f := &leaseFamily{}
	d := directoryWithFamilies(Registry{Families: []Family{f}, Timeout: time.Second})
	known := Info{Type: "push", ID: "receiver", Address: "192.0.2.1"}
	d.seen[known.id()] = observation{info: known, at: time.Now()}
	first, err := d.Connect(t.Context(), Info{Type: "push", Address: known.Address})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	moved := known
	moved.Address = "192.0.2.2"
	if _, err := d.Connect(t.Context(), moved); err == nil {
		t.Fatal("pin bypassed known identity after address change")
	}
}

func TestCanceledCommandDoesNotWaitForAnotherLeaseCommand(t *testing.T) {
	l := newLease(nil)
	l.device = idle{}
	if err := l.commands.Acquire(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	defer l.commands.Release(1)
	ctx, cancel := context.WithCancelCause(t.Context())
	failure := errors.New("caller abandoned queued pause")
	done := make(chan error, 1)
	go func() { done <- l.Pause(ctx) }()
	cancel(failure)
	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatalf("pause lost cancellation cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled pause waits behind another command")
	}
}

type cancelOnCloseDevice struct {
	idle
	started chan struct{}
	closed  chan struct{}
}

func (d *cancelOnCloseDevice) Pause(ctx context.Context) error {
	close(d.started)
	<-ctx.Done()
	return context.Cause(ctx)
}
func (d *cancelOnCloseDevice) Close() error { close(d.closed); return nil }

func TestLeaseCloseCancelsOwnedCommandsBeforeFinalCleanup(t *testing.T) {
	dir := directoryWithFamilies(Registry{})
	dev := &cancelOnCloseDevice{started: make(chan struct{}), closed: make(chan struct{})}
	l := newLease(dir)
	l.device = dev
	result := make(chan error, 1)
	go func() { result <- l.Pause(t.Context()) }()
	<-dev.started
	finished := make(chan error, 1)
	go func() { finished <- l.Close() }()
	select {
	case err := <-result:
		if !errors.Is(err, errLeaseClosed) {
			t.Fatalf("command lost owner cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not cancel owned command")
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	} // Device.Close closes a channel: running twice would panic.
	if err := l.Resume(t.Context()); !errors.Is(err, errLeaseClosed) {
		t.Fatalf("closed lease accepted command: %v", err)
	}
}

type observedDevice struct {
	idle
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
	closed   chan struct{}
}

func (*observedDevice) Position(context.Context) (Playhead, error) { return Playhead{}, ErrNoPosition }

func (d *observedDevice) AwaitEnd(ctx context.Context) error {
	close(d.started)
	<-ctx.Done()
	close(d.canceled)
	<-d.release
	return context.Cause(ctx)
}
func (d *observedDevice) Close() error { close(d.closed); return nil }

func TestLeaseCloseCancelsAndJoinsObservers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dev := &observedDevice{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
		l := newLease(directoryWithFamilies(Registry{}))
		l.device = dev
		result := make(chan error, 1)
		go func() { result <- l.AwaitEnd(t.Context()) }()
		<-dev.started
		finished := make(chan error, 1)
		go func() { finished <- l.Close() }()
		<-dev.canceled
		synctest.Wait()
		select {
		case <-dev.closed:
			t.Fatal("device closed while observer was running")
		default:
		}
		close(dev.release)
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, errLeaseClosed) {
			t.Fatalf("observation lost cancellation cause: %v", err)
		}
		if err := l.AwaitEnd(t.Context()); !errors.Is(err, errLeaseClosed) {
			t.Fatalf("closed lease accepted observer: %v", err)
		}
	})
}
