package roku

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

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
	} {
		if got, err := (Family{}).Locate(t.Context(), address); err != nil || got != want {
			t.Errorf("Locate(%q) = %q, %v, want %q", address, got, err, want)
		}
	}
}

func TestMalformedAppListDoesNotTriggerSideloading(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/query/apps" {
			t.Errorf("malformed app list triggered %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `<apps><app id="dev">Castor`)
	}))
	defer ts.Close()
	_, err := Family{Config: Config{Password: "developer-password"}}.Connect(t.Context(), device.Info{Address: ts.URL})
	if err == nil || !strings.Contains(err.Error(), "decoding roku apps") {
		t.Fatalf("Connect = %v, want the app-list decoding failure before sideloading", err)
	}
}
