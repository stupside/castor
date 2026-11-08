package airplay

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"howett.net/plist"
)

// eventChannel is the connection on which the receiver reports to castor, each report a request castor answers 200.
type eventChannel struct {
	conn net.Conn
	// gone closes once the receiver closes the channel or it fails.
	gone chan struct{}
}

// openEvents connects to the receiver's event port, sealed with keys from secret, and hands each report to handle.
// A receiver may listen a little after announcing its port, so a failed connection is retried.
func openEvents(ctx context.Context, host string, port int, secret []byte, handle func(map[string]any)) (*eventChannel, error) {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	var conn net.Conn
	var err error
	for attempt := 0; ; attempt++ {
		dialCtx, cancel := context.WithTimeout(ctx, answerWithin)
		var d net.Dialer
		conn, err = d.DialContext(dialCtx, "tcp", address)
		cancel()
		if err == nil || attempt == 4 || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("connecting to the receiver's event channel: %w", err)
	}
	write, read := eventKeys(secret)
	e := &eventChannel{conn: conn, gone: make(chan struct{})}
	s := secure(conn, nil, write, read)
	go e.serve(s, bufio.NewReader(s), handle)
	return e, nil
}

func (e *eventChannel) serve(s *secureConn, r *bufio.Reader, handle func(map[string]any)) {
	defer close(e.gone)
	for {
		req, err := readMessage(r)
		if err != nil {
			return
		}
		if req.method == "" {
			continue
		}
		answer := fmt.Sprintf("%s 200 OK\r\nContent-Length: 0\r\nAudio-Latency: 0\r\n", req.proto)
		if v := req.header.Get("Server"); v != "" {
			answer += "Server: " + v + "\r\n"
		}
		if v := req.header.Get("CSeq"); v != "" {
			answer += "CSeq: " + v + "\r\n"
		}
		if _, err := s.Write([]byte(answer + "\r\n")); err != nil {
			return
		}
		if req.uri != "/command" || len(req.body) == 0 {
			continue
		}
		if report, err := decodeCommand(req.body); err == nil {
			handle(report)
		} else {
			slog.Debug("airplay event the receiver sent is unreadable", "error", err)
		}
	}
}

func (e *eventChannel) Close() error { return e.conn.Close() }

// decodeCommand reads a /command body: a binary plist whose params.data holds the command as a nested plist, or the command itself.
func decodeCommand(body []byte) (map[string]any, error) {
	var outer map[string]any
	if _, err := plist.Unmarshal(body, &outer); err != nil {
		return nil, err
	}
	params, _ := outer["params"].(map[string]any)
	data, ok := params["data"].([]byte)
	if !ok {
		return outer, nil
	}
	var inner map[string]any
	if _, err := plist.Unmarshal(data, &inner); err != nil {
		return nil, err
	}
	return inner, nil
}

// encodeCommand wraps a command the way a remote control stream takes it: a nested plist in params.data.
func encodeCommand(command map[string]any) ([]byte, error) {
	data, err := plist.Marshal(command, plist.BinaryFormat)
	if err != nil {
		return nil, err
	}
	return plist.Marshal(map[string]any{"params": map[string]any{"data": data}}, plist.BinaryFormat)
}

// timingServer answers the receiver's NTP-style timing requests, which an NTP-timed session may send.
type timingServer struct{ conn *net.UDPConn }

func listenTiming(local net.IP) (*timingServer, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: local})
	if err != nil {
		return nil, fmt.Errorf("listening for the receiver's timing requests: %w", err)
	}
	t := &timingServer{conn: conn}
	go t.serve()
	return t, nil
}

func (t *timingServer) port() int { return t.conn.LocalAddr().(*net.UDPAddr).Port }

func (t *timingServer) serve() {
	buf := make([]byte, 128)
	for {
		n, from, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 32 {
			continue
		}
		now := ntpNow()
		reply := bytes.Join([][]byte{{buf[0], 0xd3, 0x00, 0x07}, make([]byte, 4), buf[24:32], now, now}, nil)
		_, _ = t.conn.WriteToUDP(reply, from)
	}
}

func (t *timingServer) Close() error { return t.conn.Close() }

// ntpNow is the time as NTP writes it: seconds since 1900, then the fraction of a second, both 32 bits big-endian.
func ntpNow() []byte {
	now := time.Now()
	secs := uint64(now.Unix()) + 2208988800
	frac := uint64(now.Nanosecond()) << 32 / 1e9
	out := make([]byte, 8)
	for i := range 4 {
		out[i] = byte(secs >> (24 - 8*i))
		out[4+i] = byte(frac >> (24 - 8*i))
	}
	return out
}
