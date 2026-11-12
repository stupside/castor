package airplay

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

const (
	frameLength = 1024 // HAP section 5.2.2: at most 1024 plaintext bytes per frame
	tagLength   = 16
)

// secureConn is a connection after pair-verify: every frame sealed with ChaCha20-Poly1305, its length as the AAD.
type secureConn struct {
	net.Conn
	r *bufio.Reader

	writeMu  sync.Mutex
	writeKey []byte
	written  uint64

	readKey []byte
	read    uint64
	pending []byte
}

// secure wraps conn, whose reader r may already hold bytes received after the handshake.
func secure(conn net.Conn, r *bufio.Reader, writeKey, readKey []byte) *secureConn {
	if r == nil {
		r = bufio.NewReader(conn)
	}
	return &secureConn{Conn: conn, r: r, writeKey: writeKey, readKey: readKey}
}

func (c *secureConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var out []byte
	for rest := p; len(rest) > 0; {
		n := min(len(rest), frameLength)
		length := binary.LittleEndian.AppendUint16(nil, uint16(n))
		out = append(out, length...)
		out = append(out, seal(c.writeKey, counterNonce(c.written), rest[:n], length)...)
		c.written++
		rest = rest[n:]
	}
	if _, err := c.Conn.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *secureConn) Read(p []byte) (int, error) {
	if len(c.pending) == 0 {
		var length [2]byte
		if _, err := io.ReadFull(c.r, length[:]); err != nil {
			return 0, err
		}
		n := int(binary.LittleEndian.Uint16(length[:]))
		if n > frameLength {
			return 0, fmt.Errorf("the receiver sent a %d-byte frame, over HAP's %d", n, frameLength)
		}
		sealed := make([]byte, n+tagLength)
		if _, err := io.ReadFull(c.r, sealed); err != nil {
			return 0, err
		}
		plain, err := unseal(c.readKey, counterNonce(c.read), sealed, length[:])
		if err != nil {
			return 0, fmt.Errorf("the receiver sent a frame that does not decrypt: %w", err)
		}
		c.read++
		c.pending = plain
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
