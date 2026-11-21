package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var media = Peer{Name: "the media server", TokenKey: "servers.media.token"}

func TestAPeerIsNamedForWhatItDid(t *testing.T) {
	notOurs := connect.NewError(connect.CodeInvalidArgument, errors.New("validate: field is required"))
	for _, tt := range []struct {
		name string
		err  error
		code connect.Code
		say  string
		not  string
	}{
		{"credentials it refused", connect.NewWireError(connect.CodeUnauthenticated, errors.New("bad token")), connect.CodeInternal, "check servers.media.token", ""},
		{"a caller it does not allow", connect.NewWireError(connect.CodePermissionDenied, errors.New("no")), connect.CodeInternal, "check servers.media.token", ""},
		{"a request it rejected", connect.NewWireError(connect.CodeInvalidArgument, errors.New("source: required")), connect.CodeInternal, "rejected this server's request", "check servers"},
		{"a stream cut short", connect.NewError(connect.CodeInvalidArgument, errors.Join(errors.New("protocol error: incomplete envelope"), io.ErrUnexpectedEOF)), connect.CodeUnavailable, "the media server went away", "check servers"},
		{"a cast it never heard of", connect.NewWireError(connect.CodeNotFound, errors.New("no cast")), connect.CodeNotFound, "no cast", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := media.Reported(tt.err)
			if connect.CodeOf(got) != tt.code || !strings.Contains(got.Error(), tt.say) {
				t.Errorf("answered %v (%s), want %s saying %q", got, connect.CodeOf(got), tt.code, tt.say)
			}
			if tt.not != "" && strings.Contains(got.Error(), tt.not) {
				t.Errorf("answered %v, which says %q", got, tt.not)
			}
		})
	}
	if got := media.Reported(notOurs); got != notOurs {
		t.Errorf("an invalid argument the client made itself was answered %v, want it unchanged", got)
	}
	if media.Reported(nil) != nil {
		t.Error("no error was reported as one")
	}
}

// A client of a peer hears the peer through the protocol, so these are the answers it is really given.
func TestACallToAPeerIsReportedWhereverItFails(t *testing.T) {
	refusing := func(code, message string, status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"code":"`+code+`","message":"`+message+`"}`)
		})
	}
	cut := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/connect+json")
		_, _ = w.Write([]byte{0, 0, 0, 0})
	})
	call := func(t *testing.T, h http.Handler, stream bool) error {
		server := httptest.NewServer(h)
		defer server.Close()
		client := connect.NewClient[emptypb.Empty, emptypb.Empty](server.Client(), server.URL+"/castor.media.v1.Media/Run", connect.WithProtoJSON(), Peering(media))
		if !stream {
			_, err := client.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{}))
			return err
		}
		serverStream, err := client.CallServerStream(context.Background(), connect.NewRequest(&emptypb.Empty{}))
		if err != nil {
			t.Fatal(err)
		}
		for serverStream.Receive() {
		}
		return serverStream.Err()
	}
	for _, tt := range []struct {
		name   string
		h      http.Handler
		stream bool
		code   connect.Code
		say    string
	}{
		{"a unary call it refused", refusing("unauthenticated", "bad token", http.StatusUnauthorized), false, connect.CodeInternal, "check servers.media.token"},
		{"a unary call it rejected", refusing("invalid_argument", "source: required", http.StatusBadRequest), false, connect.CodeInternal, "rejected this server's request"},
		{"a stream it cut short", cut, true, connect.CodeUnavailable, "the media server went away"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := call(t, tt.h, tt.stream)
			if connect.CodeOf(err) != tt.code || err == nil || !strings.Contains(err.Error(), tt.say) {
				t.Errorf("a call reported %v (%s), want %s saying %q", err, connect.CodeOf(err), tt.code, tt.say)
			}
		})
	}
}
