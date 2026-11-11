package dlna

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

const (
	searchTarget  = "urn:schemas-upnp-org:device:MediaRenderer:1"
	ssdpPort      = "1900"
	searchTimeout = 3 * time.Second
)

// ssdpTarget is address on SSDP's own port; a typed port is the device's HTTP server.
func ssdpTarget(address string) string {
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	return device.WithPort(address, ssdpPort)
}

// searchDescription: unicast M-SEARCH of target for description (no interface enumeration).
func searchDescription(ctx context.Context, target string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "udp", target)
	if err != nil {
		return "", fmt.Errorf("dialing %s: %w", target, err)
	}
	defer conn.Close()
	// Closing the connection on ctx is what ends a read at its deadline too.
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()

	msearch := strings.Join([]string{
		"M-SEARCH * HTTP/1.1",
		"HOST: " + target,
		`MAN: "ssdp:discover"`,
		"MX: 1",
		"ST: " + searchTarget,
		"", "",
	}, "\r\n")
	if _, err := conn.Write([]byte(msearch)); err != nil {
		return "", fmt.Errorf("sending M-SEARCH: %w", err)
	}

	// A device answers once per matching root/embedded device.
	buf := make([]byte, 2048)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return "", context.Cause(ctx)
			}
			return "", fmt.Errorf("awaiting SSDP response: %w", err)
		}
		if location := parseSSDPLocation(buf[:n]); location != "" {
			return location, nil
		}
	}
}

// parseSSDPLocation matches case-insensitively, per RFC 2616, and returns "" when absent.
func parseSSDPLocation(response []byte) string {
	for line := range strings.SplitSeq(string(response), "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "LOCATION") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
