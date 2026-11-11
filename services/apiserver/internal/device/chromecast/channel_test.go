package chromecast

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"testing/synctest"

	castmedia "github.com/vishen/go-chromecast/cast"
)

func TestAReceiverThatAnswersNoPingIsDroppedAfterThreeMissedBeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		near, far := net.Pipe()
		sender := newChannel(near, func([]byte) {})
		// Reads every frame and answers none.
		silent := &channel{conn: far, gone: make(chan struct{})}
		go func() {
			for {
				if _, err := silent.receive(); err != nil {
					return
				}
			}
		}()

		synctest.Sleep((heartbeatMisses - 1) * heartbeatEvery)
		select {
		case <-sender.gone:
			t.Fatal("the connection was dropped before the receiver missed its beats")
		default:
		}
		synctest.Sleep(2 * heartbeatEvery)
		select {
		case <-sender.gone:
		default:
			t.Fatal("a receiver that answered no PING kept its connection")
		}
		_ = far.Close()
	})
}

func TestAReceiverThatAnswersEachPingKeepsItsConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		near, far := net.Pipe()
		sender := newChannel(near, func([]byte) {})
		peer := &channel{conn: far, writing: make(chan struct{}, 1)}
		go func() {
			for {
				if _, err := peer.receive(); err != nil {
					return
				}
				go func() {
					_ = peer.send(context.Background(), senderID, nsHeartbeat, &castmedia.PayloadHeader{Type: msgPong})
				}()
			}
		}()

		synctest.Sleep(10 * heartbeatMisses * heartbeatEvery)
		select {
		case <-sender.gone:
			t.Fatal("a receiver answering its pings was dropped")
		default:
		}
		_ = sender.Close()
		_ = far.Close()
	})
}

func TestAReceiversErrorCodeIsLoggedAsItArrives(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	near, far := net.Pipe()
	observed := make(chan struct{})
	sender := newChannel(near, func([]byte) { observed <- struct{}{} })
	t.Cleanup(func() { _ = sender.Close(); _ = far.Close() })
	peer := &channel{conn: far, writing: make(chan struct{}, 1)}
	for _, message := range []any{map[string]any{"type": "ERROR", "detailedErrorCode": 104}, castmedia.PayloadHeader{Type: msgMediaStatus}} {
		if err := peer.send(t.Context(), senderID, nsMedia, message); err != nil {
			t.Fatal(err)
		}
		<-observed
	}
	if got := logged.String(); strings.Count(got, "detailed_error_code=") != 1 || !strings.Contains(got, "detailed_error_code=104") {
		t.Errorf("logged %q, want the receiver's detailed error code 104 once", got)
	}
}
