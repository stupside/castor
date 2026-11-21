package mediaclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// The media server killed mid-stream leaves a frame cut short, which the client reports as the server gone.
func TestAMediaServerKilledMidStreamIsReportedAsGone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/connect+proto")
		_, _ = w.Write([]byte{0, 0, 0, 0})
	}))
	defer server.Close()
	_, err := New(server.Client(), server.URL).Watch(t.Context(), "cast", func(*castorv1.CastStatus) {})
	if err == nil || !strings.Contains(err.Error(), "the media server went away") {
		t.Errorf("a stream cut in its first frame was reported %v, want the media server gone", err)
	}
}
