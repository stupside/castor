package device

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// stub is a family that locates every address as given and connects nothing.
type stub struct{}

func (stub) Type() Type { return "push" }

func (stub) Discover(context.Context) []Info { return nil }

func (stub) Locate(_ context.Context, address string) (string, error) { return address, nil }

func (stub) Connect(context.Context, Info) (Device, error) {
	return nil, errors.New("stub devices do not connect")
}

func TestAPinnedDeviceWithNoNameIsNamedByItsHost(t *testing.T) {
	for _, tt := range []struct {
		target Info
		want   string
	}{
		{Info{Type: "push", Address: "http://192.168.0.5:9197/desc.xml"}, "192.168.0.5"},
		{Info{Type: "push", Address: "192.168.0.3:8060", Name: "Bedroom"}, "Bedroom"},
	} {
		info, err := locate(t.Context(), stub{}, tt.target, time.Second)
		if err != nil {
			t.Fatalf("locate(%+v) error = %v", tt.target, err)
		}
		if info != (Info{Name: tt.want, Type: "push", Address: tt.target.Address}) {
			t.Errorf("locate(%+v) = %+v, want it labelled %q", tt.target, info, tt.want)
		}
	}
}

// echoing is a family whose devices answer every sweep more than once, as SSDP and mDNS announce.
type echoing struct{ stub }

func (echoing) Discover(context.Context) []Info {
	bedroom := Info{ID: "uuid-1", Name: "Bedroom", Type: "push", Address: "10.0.0.9"}
	kitchen := Info{ID: "uuid-2", Name: "Kitchen", Type: "push", Address: "10.0.0.8"}
	return []Info{bedroom, kitchen, bedroom}
}

func TestADeviceAnnouncedTwiceIsListedOnce(t *testing.T) {
	found := Registry{Families: []Family{echoing{}}, Timeout: time.Second}.Discover(t.Context())
	if len(found) != 2 || found[0].ID != "uuid-1" || found[1].ID != "uuid-2" {
		t.Errorf("Discover = %+v, want Bedroom then Kitchen, once each", found)
	}
}

type unresponsive struct{ stub }

func (unresponsive) Connect(ctx context.Context, _ Info) (Device, error) {
	<-ctx.Done()
	return nil, context.Cause(ctx)
}

func TestConnectingAnUnresponsiveDeviceUsesTheNetworkBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		start := time.Now()
		_, err := (Registry{Families: []Family{unresponsive{}}, Timeout: time.Second}).Connect(ctx, Info{Type: "push", Address: "10.0.0.9"})
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != time.Second {
			t.Fatalf("Connect = %v after %s, want deadline exceeded within the one-second network budget", err, time.Since(start))
		}
	})
}
