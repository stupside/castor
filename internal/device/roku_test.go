package device

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stupside/castor/internal/device/rokuchannel"
	"github.com/stupside/castor/internal/media"
)

func TestRokuInfo(t *testing.T) {
	tests := []struct {
		name     string
		location string
		devName  string
		want     Info
		ok       bool
	}{
		{
			name:     "ecp root with friendly name",
			location: "http://192.0.2.10:8060/",
			devName:  "Living Room",
			want:     Info{Name: "Living Room", Type: TypeRoku, Address: "http://192.0.2.10:8060/"},
			ok:       true,
		},
		{
			name:     "empty name falls back to host",
			location: "http://192.0.2.11:8060/",
			devName:  "",
			want:     Info{Name: "192.0.2.11", Type: TypeRoku, Address: "http://192.0.2.11:8060/"},
			ok:       true,
		},
		{
			name:     "unparseable location is rejected",
			location: "://nope",
			devName:  "x",
			ok:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rokuInfo(tt.location, tt.devName)
			if ok != tt.ok {
				t.Fatalf("rokuInfo() ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("rokuInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestStreamFormatFor(t *testing.T) {
	tests := []struct {
		contentType string
		want        string
	}{
		{media.HLS, "hls"},
		{media.MP4, "mp4"},
		{media.MKV, "mkv"},
		{media.WebM, "hls"}, // unknown → lenient fallback, not an error
		{"", "hls"},
	}
	for _, tt := range tests {
		if got := streamFormatFor(tt.contentType); got != tt.want {
			t.Errorf("streamFormatFor(%q) = %q, want %q", tt.contentType, got, tt.want)
		}
	}
}

func TestParseDeviceInfoName(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "prefers user-device-name",
			body: `<device-info><user-device-name>Bedroom</user-device-name><model-name>Roku Ultra</model-name></device-info>`,
			want: "Bedroom",
		},
		{
			name: "falls back to model-name when user name blank",
			body: `<device-info><user-device-name></user-device-name><model-name>Roku Ultra</model-name></device-info>`,
			want: "Roku Ultra",
		},
		{
			name: "invalid xml yields empty",
			body: `not xml`,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDeviceInfoName([]byte(tt.body)); got != tt.want {
				t.Errorf("parseDeviceInfoName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDevChannelInstalled(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "dev channel present",
			body: `<apps><app id="dev" type="appl" version="1.0">Castor</app><app id="12">Netflix</app></apps>`,
			want: true,
		},
		{
			name: "no dev channel",
			body: `<apps><app id="12">Netflix</app><app id="13">YouTube</app></apps>`,
			want: false,
		},
		{
			name: "invalid xml",
			body: `<broken`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := devChannelInstalled([]byte(tt.body)); got != tt.want {
				t.Errorf("devChannelInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRokuPlayLaunchRequest(t *testing.T) {
	var gotPath, gotURL, gotFormat, gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotURL = r.URL.Query().Get(rokuchannel.ParamURL)
		gotFormat = r.URL.Query().Get(rokuchannel.ParamFormat)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	dev := &rokuDevice{ecp: mustParseURL(t, ts.URL), appID: "dev", hc: ts.Client()}
	stream := mustParseURL(t, "http://192.0.2.99:1234/stream.m3u8")

	if err := dev.Play(context.Background(), stream, media.HLS); err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/launch/dev" {
		t.Errorf("path = %q, want /launch/dev", gotPath)
	}
	if gotURL != stream.String() {
		t.Errorf("%s = %q, want %q", rokuchannel.ParamURL, gotURL, stream.String())
	}
	if gotFormat != "hls" {
		t.Errorf("%s = %q, want hls", rokuchannel.ParamFormat, gotFormat)
	}
}

func TestRokuPlayNon2xxErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dev := &rokuDevice{ecp: mustParseURL(t, ts.URL), appID: "dev", hc: ts.Client()}
	stream := mustParseURL(t, "http://192.0.2.99:1234/stream.m3u8")
	if err := dev.Play(context.Background(), stream, media.HLS); err == nil {
		t.Fatal("Play() to a 404 launch endpoint should error")
	}
}

func TestInstallOutcome(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"install success", `<font color="green">Install Success.</font>`, false},
		{"identical version", `Identical to previous version -- not replacing.`, false},
		{"failure", `<font color="red">Install Failure: Compilation Failed.</font>`, true},
		{"error", `Error: bad zip`, true},
		{"unrecognized 2xx", `<html>ok</html>`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := installOutcome([]byte(tt.body))
			if (err != nil) != tt.wantErr {
				t.Errorf("installOutcome() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestInstallChannelDigestUpload drives installChannel against a server that
// issues a Digest challenge, then asserts the retried request carries the
// multipart archive and mysubmit field. It verifies the digest handshake happens
// (the first, unauthenticated request is challenged) and that our upload shape is
// correct — the parts CI can check without a real Roku.
func TestInstallChannelDigestUpload(t *testing.T) {
	var authedArchiveLen int
	var gotSubmit string
	var challenged, authed bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			challenged = true
			w.Header().Set("WWW-Authenticate", `Digest realm="rt", nonce="abc123", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		authed = true
		if r.URL.Path != "/plugin_install" {
			t.Errorf("path = %q, want /plugin_install", r.URL.Path)
		}
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parsing content type: %v", err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("reading part: %v", err)
			}
			switch part.FormName() {
			case "mysubmit":
				b, _ := io.ReadAll(part)
				gotSubmit = string(b)
			case "archive":
				b, _ := io.ReadAll(part)
				authedArchiveLen = len(b)
			}
		}
		_, _ = io.WriteString(w, "Install Success.")
	}))
	defer ts.Close()

	err := installChannel(context.Background(), ts.URL+"/plugin_install", "rokudev", "secret", []byte("PK\x03\x04fake-zip-bytes"))
	if err != nil {
		t.Fatalf("installChannel() error = %v", err)
	}
	if !challenged || !authed {
		t.Errorf("digest handshake incomplete: challenged=%v authed=%v", challenged, authed)
	}
	if gotSubmit != "Install" {
		t.Errorf("mysubmit = %q, want Install", gotSubmit)
	}
	if authedArchiveLen == 0 {
		t.Error("archive part was empty on the authenticated request")
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	// Play/ecpPost set Path per request, so keep only scheme+host like connectRoku.
	if strings.Contains(s, "://") && u.Path == "/" {
		u.Path = ""
	}
	return u
}
