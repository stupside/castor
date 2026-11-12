package airplay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"howett.net/plist"
)

const (
	protoRTSP = "RTSP/1.0"
	protoHTTP = "HTTP/1.1"

	contentBplist = "application/x-apple-binary-plist"
	contentTLV    = "application/octet-stream"

	userAgent = "AirPlay/550.10"

	// maxBody bounds what castor reads of one message from a receiver.
	maxBody = 8 << 20
)

// message is one request or response on an AirPlay connection, whose first line says which.
type message struct {
	proto string
	// code and status are a response's.
	code   int
	status string
	// method and uri are a request's.
	method, uri string
	header      textproto.MIMEHeader
	body        []byte
}

func readMessage(r *bufio.Reader) (*message, error) {
	tp := textproto.NewReader(r)
	line, err := tp.ReadLine()
	if err != nil {
		return nil, err
	}
	m := &message{}
	first, rest, _ := strings.Cut(line, " ")
	if strings.HasPrefix(first, "RTSP/") || strings.HasPrefix(first, "HTTP/") {
		code, status, _ := strings.Cut(rest, " ")
		if m.code, err = strconv.Atoi(code); err != nil {
			return nil, fmt.Errorf("malformed status line %q", line)
		}
		m.proto, m.status = first, status
	} else {
		uri, proto, ok := strings.Cut(rest, " ")
		if !ok {
			return nil, fmt.Errorf("malformed request line %q", line)
		}
		m.method, m.uri, m.proto = first, uri, proto
	}
	if m.header, err = tp.ReadMIMEHeader(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if v := m.header.Get("Content-Length"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxBody {
			return nil, fmt.Errorf("unacceptable Content-Length %q", v)
		}
		m.body = make([]byte, n)
		if _, err := io.ReadFull(r, m.body); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// decode reads the message's binary plist body into v.
func (m *message) decode(v any) error {
	if len(m.body) == 0 {
		return errors.New("the receiver answered with no body")
	}
	if _, err := plist.Unmarshal(m.body, v); err != nil {
		return fmt.Errorf("the receiver answered with an unreadable plist: %w", err)
	}
	return nil
}

// link is the control connection to a receiver: plain until pair-verify, sealed after it.
type link struct {
	conn net.Conn
	// address is where the receiver answers, host:port.
	address string

	mu  sync.Mutex
	rw  io.ReadWriter
	r   *bufio.Reader
	seq int

	session      uint32
	dacpID       string
	activeRemote string
}

// answerWithin bounds each exchange with a receiver, and every dial.
const answerWithin = 10 * time.Second

func dial(ctx context.Context, address string) (*link, error) {
	dialCtx, cancel := context.WithTimeout(ctx, answerWithin)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", address)
	if err != nil {
		return nil, err
	}
	var id [12]byte
	_, _ = rand.Read(id[:])
	return &link{
		conn: conn, address: address, rw: conn, r: bufio.NewReader(conn),
		session:      binary.BigEndian.Uint32(id[:4]),
		dacpID:       strings.ToUpper(hex.EncodeToString(id[4:])),
		activeRemote: strconv.FormatUint(uint64(binary.BigEndian.Uint32(id[8:])), 10),
	}, nil
}

// host is the receiver's address without its port.
func (l *link) host() string {
	host, _, err := net.SplitHostPort(l.address)
	if err != nil {
		return l.address
	}
	return host
}

func (l *link) localIP() net.IP {
	if a, ok := l.conn.LocalAddr().(*net.TCPAddr); ok {
		return a.IP
	}
	return nil
}

// encrypt seals the link from here on with the keys pair-verify derived.
func (l *link) encrypt(writeKey, readKey []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := secure(l.conn, l.r, writeKey, readKey)
	l.rw, l.r = s, bufio.NewReader(s)
}

func (l *link) Close() error { return l.conn.Close() }

// sessionURI is the URI RTSP requests that name no resource are sent to.
func (l *link) sessionURI() string {
	return "rtsp://" + l.localIP().String() + "/" + strconv.FormatUint(uint64(l.session), 10)
}

type request struct {
	method, uri, proto string
	header             map[string]string
	contentType        string
	body               []byte
}

// exchange sends req and reads the receiver's answer, one exchange at a time.
func (l *link) exchange(ctx context.Context, req request) (*message, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	deadline := time.Now().Add(answerWithin)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = l.conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = l.conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	proto := req.proto
	if proto == "" {
		proto = protoRTSP
	}
	uri := req.uri
	if uri == "" {
		uri = l.sessionURI()
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s %s\r\n", req.method, uri, proto)
	header := map[string]string{"User-Agent": userAgent}
	if proto == protoRTSP {
		header["CSeq"] = strconv.Itoa(l.seq)
		header["DACP-ID"] = l.dacpID
		header["Active-Remote"] = l.activeRemote
		header["Client-Instance"] = l.dacpID
		l.seq++
	}
	if req.contentType != "" {
		header["Content-Type"] = req.contentType
	}
	maps.Copy(header, req.header)
	header["Content-Length"] = strconv.Itoa(len(req.body))
	for k, v := range header {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	b.WriteString("\r\n")
	b.Write(req.body)
	if _, err := l.rw.Write(b.Bytes()); err != nil {
		return nil, l.failed(ctx, err)
	}
	resp, err := readMessage(l.r)
	if err != nil {
		return nil, l.failed(ctx, err)
	}
	if resp.method != "" {
		return nil, fmt.Errorf("the receiver sent a request (%s %s) where castor waited for its answer", resp.method, resp.uri)
	}
	return resp, nil
}

func (l *link) failed(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

// exchangePlist sends v as a binary plist body.
func (l *link) exchangePlist(ctx context.Context, method, uri, proto string, header map[string]string, v any) (*message, error) {
	body, err := plist.Marshal(v, plist.BinaryFormat)
	if err != nil {
		return nil, err
	}
	return l.exchange(ctx, request{method: method, uri: uri, proto: proto, header: header, contentType: contentBplist, body: body})
}

// statusError is a receiver's refusal, answered with the status code it sent.
type statusError struct {
	what string
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("the receiver refused %s with status %d", e.what, e.code)
}

// ok fails a response that is not a success.
func ok(resp *message, what string) error {
	if resp.code >= 200 && resp.code < 300 {
		return nil
	}
	return &statusError{what: what, code: resp.code}
}
