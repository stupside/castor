package dlna

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
)

// answerSearches answers unicast M-SEARCHes on SSDP's own port with the description's LOCATION, as a device's SSDP stack does, and pins its HTTP host:port.
func answerSearches(t *testing.T, description string) (string, error) {
	u, err := url.Parse(description)
	if err != nil {
		return "", err
	}
	// The port is shared with any SSDP stack already on this machine, which listens on every address, not this one.
	listen := net.ListenConfig{Control: sharePort}
	conn, err := listen.ListenPacket(t.Context(), "udp4", "127.0.0.1:1900")
	if err != nil {
		return "", fmt.Errorf("listening for SSDP searches: %w", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { conn.Close(); <-done })
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if !strings.HasPrefix(string(buf[:n]), "M-SEARCH") {
				continue
			}
			reply := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nEXT:\r\nLOCATION: " + description +
				"\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\nUSN: uuid:castor-e2e::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n"
			_, _ = conn.WriteTo([]byte(reply), from)
		}
	}()
	return u.Host, nil
}
