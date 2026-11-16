package roku

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

type deviceInfoRoundTripper func(*http.Request) (*http.Response, error)

func (f deviceInfoRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDiscoveryNamesARokuByItsOwnerSetName(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	client := &http.Client{Transport: deviceInfoRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/query/device-info" {
			t.Errorf("asked %s for a name", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<device-info><user-device-name>Living Room</user-device-name></device-info>`))}, nil
	})}
	found := discover(ctx, func(context.Context) ([]*http.Response, error) {
		return []*http.Response{{Header: http.Header{"Location": {"http://roku.test:8060/"}, "Usn": {"uuid:roku-1"}}}}, nil
	}, client)
	if len(found) != 1 || found[0].Name != "Living Room" || found[0].ID != "uuid:roku-1" {
		t.Fatalf("discovery returned %+v, want the owner-set Roku name", found)
	}
}

func TestConnectVerifiesTheChannelItWillLaunch(t *testing.T) {
	for _, tt := range []struct {
		name, apps, appID string
		wantErr           bool
	}{
		{name: "castor's dev channel is installed", apps: `<apps><app id="dev">Castor</app></apps>`},
		{name: "a foreign dev channel and no password", apps: `<apps><app id="dev">SomeoneElse</app></apps>`, wantErr: true},
		{name: "a published app that is not installed", apps: `<apps><app id="12345">MyChannel</app></apps>`, appID: "99999", wantErr: true},
		{name: "a published app is installed", apps: `<apps><app id="12345">MyChannel</app></apps>`, appID: "12345"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/query/apps" {
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, tt.apps)
			}))
			defer ts.Close()
			_, err := Family{Config: Config{AppID: tt.appID}}.Connect(t.Context(), device.Info{Name: "Bedroom Roku", Type: familyType, Address: ts.URL})
			if (err != nil) != tt.wantErr {
				t.Errorf("Connect() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLocate(t *testing.T) {
	for address, want := range map[string]string{
		"192.168.0.3":              "http://192.168.0.3:8060",
		"http://192.168.0.3:8888/": "http://192.168.0.3:8888",
		"[2001:db8::3]":            "http://[2001:db8::3]:8060",
	} {
		if got, err := (Family{}).Locate(t.Context(), address); err != nil || got != want {
			t.Errorf("Locate(%q) = %q, %v, want %q", address, got, err, want)
		}
	}
}

func TestMalformedAppListDoesNotTriggerSideloading(t *testing.T) {
	for _, body := range []string{`<apps><app id="dev">Castor`, `<html><body>Device login required</body></html>`} {
		t.Run(body, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/query/apps" {
					t.Errorf("malformed app list triggered %s %s", r.Method, r.URL.Path)
				}
				_, _ = io.WriteString(w, body)
			}))
			defer ts.Close()
			_, err := Family{Config: Config{Password: "developer-password"}}.Connect(t.Context(), device.Info{Address: ts.URL})
			if err == nil || !strings.Contains(err.Error(), "decoding roku apps") {
				t.Fatalf("Connect = %v, want the app-list decoding failure before sideloading", err)
			}
		})
	}
}
