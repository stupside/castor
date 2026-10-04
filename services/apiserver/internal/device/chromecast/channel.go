package chromecast

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	castmedia "github.com/vishen/go-chromecast/cast"
	pb "github.com/vishen/go-chromecast/cast/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
)

// The Cast protocol's message types, and the player state and reason a failed load reports.
const (
	msgConnect     = "CONNECT"
	msgGetStatus   = "GET_STATUS"
	msgLoad        = "LOAD"
	msgClose       = "CLOSE"
	msgMediaStatus = "MEDIA_STATUS"
	msgPing        = "PING"
	msgPong        = "PONG"
	stateIdle      = "IDLE"
	idleError      = "ERROR"
)

const (
	nsConnection = "urn:x-cast:com.google.cast.tp.connection"
	nsHeartbeat  = "urn:x-cast:com.google.cast.tp.heartbeat"
	nsReceiver   = "urn:x-cast:com.google.cast.receiver"
	nsMedia      = "urn:x-cast:com.google.cast.media"

	senderID   = "sender-0"
	receiverID = "receiver-0"

	// A receiver answers LOAD only once its player has opened the media, which is not instant.
	answerWithin = 20 * time.Second

	// The Cast v2 protocol caps a message at 64 KiB.
	maxFrame = 64 << 10
)

// wire encodes go-chromecast's payloads, tagged for v1, as devices have always been sent them: no zero requestId, nil slices as null.
var wire = json.JoinOptions(jsonv1.OmitEmptyWithLegacySemantics(true), json.FormatNilSliceAsNull(true))

// channel is one Cast v2 connection: its reader alone delivers what arrives, so closing it never races a send.
type channel struct {
	conn    net.Conn
	observe func(payload []byte)
	writing chan struct{}

	mu      sync.Mutex
	next    int
	waiting map[int]chan []byte

	gone      chan struct{}
	closeOnce sync.Once
}

// dial opens the channel; observe sees every message the receiver sends, from the reader's goroutine.
func dial(ctx context.Context, address string, observe func([]byte)) (*channel, error) {
	// Cast devices present certificates no public root signs.
	dialer := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return newChannel(conn, observe), nil
}

func newChannel(conn net.Conn, observe func([]byte)) *channel {
	ch := &channel{conn: conn, observe: observe, writing: make(chan struct{}, 1), waiting: map[int]chan []byte{}, gone: make(chan struct{})}
	go ch.read()
	return ch
}

func (ch *channel) read() {
	defer close(ch.gone)
	defer ch.closeOnce.Do(func() { _ = ch.conn.Close() })
	for {
		msg, err := ch.receive()
		if err != nil {
			return
		}
		payload := []byte(msg.GetPayloadUtf8())
		var header castmedia.PayloadHeader
		if json.Unmarshal(payload, &header) != nil {
			continue
		}
		if msg.GetNamespace() == nsHeartbeat && header.Type == msgPing {
			_ = ch.send(context.Background(), msg.GetSourceId(), nsHeartbeat, &castmedia.PayloadHeader{Type: msgPong})
			continue
		}
		ch.observe(payload)
		ch.answer(header.RequestId, payload)
	}
}

func (ch *channel) receive() (*pb.CastMessage, error) {
	var length uint32
	if err := binary.Read(ch.conn, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	if length > maxFrame {
		return nil, fmt.Errorf("cast frame of %d bytes exceeds the protocol's %d", length, maxFrame)
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(ch.conn, frame); err != nil {
		return nil, err
	}
	var msg pb.CastMessage
	if err := proto.Unmarshal(frame, protoadapt.MessageV2Of(&msg)); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (ch *channel) answer(requestID int, payload []byte) {
	ch.mu.Lock()
	reply, ok := ch.waiting[requestID]
	delete(ch.waiting, requestID)
	ch.mu.Unlock()
	if ok {
		reply <- payload
	}
}

// request sends payload under a fresh request id and returns the receiver's answer to it.
func (ch *channel) request(ctx context.Context, destination, namespace string, payload castmedia.Payload) ([]byte, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, answerWithin, fmt.Errorf("the chromecast did not answer within %s", answerWithin))
	defer cancel()
	reply := make(chan []byte, 1)
	ch.mu.Lock()
	ch.next++
	id := ch.next
	ch.waiting[id] = reply
	ch.mu.Unlock()
	defer func() {
		ch.mu.Lock()
		delete(ch.waiting, id)
		ch.mu.Unlock()
	}()

	payload.SetRequestId(id)
	if err := ch.send(ctx, destination, namespace, payload); err != nil {
		return nil, err
	}
	select {
	case body := <-reply:
		return body, nil
	case <-ch.gone:
		return nil, errors.New("the chromecast closed the connection before answering")
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (ch *channel) send(ctx context.Context, destination, namespace string, payload any) error {
	ctx, cancel := context.WithTimeout(ctx, answerWithin)
	defer cancel()
	body, err := json.Marshal(payload, wire)
	if err != nil {
		return err
	}
	frame, err := proto.Marshal(protoadapt.MessageV2Of(&pb.CastMessage{
		ProtocolVersion: pb.CastMessage_CASTV2_1_0.Enum(),
		SourceId:        new(senderID),
		DestinationId:   &destination,
		Namespace:       &namespace,
		PayloadType:     pb.CastMessage_STRING.Enum(),
		PayloadUtf8:     new(string(body)),
	}))
	if err != nil {
		return err
	}
	select {
	case ch.writing <- struct{}{}:
		defer func() { <-ch.writing }()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	deadline, _ := ctx.Deadline()
	if err := ch.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	// Cancellation must interrupt a blocked write, and its callback must finish
	// before the next sender installs its own deadline.
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = ch.conn.SetWriteDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
		_ = ch.conn.SetWriteDeadline(time.Time{})
	}()
	packet := append(binary.BigEndian.AppendUint32(nil, uint32(len(frame))), frame...)
	n, err := ch.conn.Write(packet)
	if n > 0 && n < len(packet) {
		// A partial frame cannot be retried on this connection.
		ch.closeOnce.Do(func() { _ = ch.conn.Close() })
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

// Close returns once the reader has stopped, so nothing is observed after it.
func (ch *channel) Close() error {
	var err error
	ch.closeOnce.Do(func() { err = ch.conn.Close() })
	<-ch.gone
	return err
}
