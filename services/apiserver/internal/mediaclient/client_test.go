package mediaclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/mediaclient"
)

type malformedCastService struct {
	mediav1connect.UnimplementedCastServiceHandler
}

func (*malformedCastService) Start(context.Context, *mediav1.StartRequest) (*mediav1.StartResponse, error) {
	return &mediav1.StartResponse{}, nil
}

func TestAnEmptyMediaCastIDIsNotAcceptedAsAStartedCast(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle(mediav1connect.NewCastServiceHandler(&malformedCastService{}))
	srv := httptest.NewTestServer(t, mux)
	c := mediaclient.New(srv.Client(), srv.URL)
	id, err := c.Start(t.Context(), &mediav1.Source{Source: &mediav1.Source_Stream{Stream: &castorv1.Stream{Url: "https://media.example/video"}}}, asked)
	if connect.CodeOf(err) != connect.CodeInternal || id != "" {
		t.Fatalf("Start = %q, %v; accepted a cast that cannot be watched or stopped", id, err)
	}
}
