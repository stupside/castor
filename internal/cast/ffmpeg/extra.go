package ffmpeg

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
)

// ExtraOutput is ffmpeg's second output stream (the PCM tee, or the -progress
// feed), read by castor while the primary output flows on stdout.
//
// It is a loopback TCP socket ffmpeg connects to rather than an inherited fd
// (pipe:3), because inheriting anything past stdio is Unix-only: os/exec
// refuses ExtraFiles on Windows outright. One transport on every platform
// keeps the path Windows runs the same path every test here runs. The
// semantics a pipe gave are kept: ffmpeg's writes block while castor is not
// reading, and the reader sees EOF once ffmpeg exits.
//
// The first connection is taken as ffmpeg's. Only a process on this host can
// race it, in the moment between listen and ffmpeg's connect, and what it
// could inject is audio to transcribe or positions for the cue writer: this
// cast's subtitle text and timing, never anything castor executes or serves
// as media.
//
// Create one with NewExtraOutput, route an ffmpeg output to URL, start the
// process WithExtraOutput, and read it here.
type ExtraOutput struct {
	url string

	// accepted is closed once the single connection has been accepted, or
	// accepting has been abandoned (conn stays nil).
	accepted chan struct{}
	conn     net.Conn

	// stopAccepting closes the listener, once.
	stopAccepting func()
}

// NewExtraOutput listens on an ephemeral loopback port and accepts exactly
// one connection: the ffmpeg output routed to URL. The kernel completes the
// connect before Accept runs, so ffmpeg never waits on castor to open it.
func NewExtraOutput() (*ExtraOutput, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("extra output listener: %w", err)
	}
	o := &ExtraOutput{
		url: (&url.URL{
			Scheme: "tcp",
			Host:   ln.Addr().String(),
			// The -progress feed is a small block every 0.1s that places
			// burned-in cues, so it must not sit in Nagle's buffer.
			RawQuery: "tcp_nodelay=1",
		}).String(),
		accepted:      make(chan struct{}),
		stopAccepting: sync.OnceFunc(func() { _ = ln.Close() }),
	}
	go func() {
		defer close(o.accepted)
		conn, err := ln.Accept()
		o.stopAccepting()
		if err == nil {
			o.conn = conn
		}
	}()
	return o, nil
}

// URL is where ffmpeg writes this output.
func (o *ExtraOutput) URL() string { return o.url }

// Read blocks until ffmpeg has connected, then reads what it wrote. An
// ffmpeg that exits without ever opening the output reads as EOF, as an
// unopened pipe's write end did.
func (o *ExtraOutput) Read(p []byte) (int, error) {
	<-o.accepted
	if o.conn == nil {
		return 0, io.EOF
	}
	n, err := o.conn.Read(p)
	if errors.Is(err, net.ErrClosed) {
		err = io.EOF
	}
	return n, err
}

// Close stops accepting and closes the connection, unblocking any Read. It
// is safe to call more than once.
func (o *ExtraOutput) Close() error {
	o.stopAccepting()
	<-o.accepted
	if o.conn == nil {
		return nil
	}
	if err := o.conn.Close(); !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
