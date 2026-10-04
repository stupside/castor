package dlna

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	searchTarget = "urn:schemas-upnp-org:device:MediaRenderer:1"
	ssdpPort     = "1900"
	// msearchTimeout bounds the unicast M-SEARCH when the caller sets no deadline.
	msearchTimeout = 3 * time.Second
)

// searchDescription: unicast M-SEARCH for description (no interface enumeration).
func searchDescription(ctx context.Context, host string) (string, error) {
	target := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		target = net.JoinHostPort(host, ssdpPort)
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "udp4", target)
	if err != nil {
		return "", fmt.Errorf("dialing %s: %w", target, err)
	}
	defer conn.Close()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(msearchTimeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}

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
