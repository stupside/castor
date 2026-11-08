package dlna

import (
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/soap"
)

// pickyRenderer refuses to seek while paused, as LG's does, and records what it was asked.
func pickyRenderer(actions *[]string) *session {
	state := "PAUSED_PLAYBACK"
	client := soap.NewSOAPClient(url.URL{Scheme: "http", Host: "192.0.2.10", Path: "/control"})
	client.HTTPClient.Transport = actionTransport(func(r *http.Request) (*http.Response, error) {
		var action string
		for name, values := range r.Header {
			if strings.EqualFold(name, "SOAPAction") && len(values) > 0 {
				action = strings.Split(strings.Trim(values[0], `"`), "#")[1]
			}
		}
		*actions = append(*actions, action)
		var answer string
		switch action {
		case "Seek":
			if state == "PAUSED_PLAYBACK" {
				body := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>501</errorCode><errorDescription>Action Failed</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`
				return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
			}
		case "Play":
			state = "PLAYING"
		case "Pause":
			state = "PAUSED_PLAYBACK"
		case "GetTransportInfo":
			answer = "<CurrentTransportState>" + state + "</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"
		}
		body := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:` + action + `Response xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` + answer + `</u:` + action + `Response></s:Body></s:Envelope>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	s := &session{transport: goupnp.ServiceClient{SOAPClient: client, Service: &goupnp.Service{ServiceType: "urn:schemas-upnp-org:service:AVTransport:1"}}}
	s.seekable.Store(true)
	return s
}

func TestAPausedFileIsSoughtWhileItPlaysAndHeldAgain(t *testing.T) {
	var actions []string
	s := pickyRenderer(&actions)
	s.file.Store(true)
	if err := s.Seek(t.Context(), 80*time.Second); err != nil {
		t.Fatalf("Seek() = %v; actions %v", err, actions)
	}
	if want := []string{"Seek", "GetTransportInfo", "Play", "GetTransportInfo", "Seek", "Pause"}; !slices.Equal(actions, want) {
		t.Fatalf("actions %v, want %v", actions, want)
	}
}

func TestAPausedStreamCastorMakesIsNotPlayedToSeek(t *testing.T) {
	var actions []string
	s := pickyRenderer(&actions)
	if err := s.Seek(t.Context(), 80*time.Second); err == nil {
		t.Fatal("Seek() succeeded on a renderer that refused it")
	}
	if slices.Contains(actions, "Play") {
		t.Fatalf("actions %v: a stream castor makes was played to seek it", actions)
	}
}
