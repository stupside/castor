package transport

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
)

// Authorized lets through only requests carrying token, when there is one; health checks need none.
func Authorized(h http.Handler, token string) http.Handler {
	if token == "" {
		return h
	}
	refusal := connect.NewErrorWriter()
	health := "/" + grpchealth.HealthV1ServiceName + "/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, got, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if strings.HasPrefix(r.URL.Path, health) || strings.EqualFold(scheme, "Bearer") && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
			h.ServeHTTP(w, r)
			return
		}
		_ = refusal.Write(w, r, connect.NewError(connect.CodeUnauthenticated, errors.New("this server asks for its bearer token")))
	})
}

// Bearer is a client carrying token on every request, when there is one.
func Bearer(token string) *http.Client {
	if token == "" {
		return http.DefaultClient
	}
	return &http.Client{
		Transport: bearing{token: token, next: http.DefaultTransport},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// The transport adds credentials itself, so HTTP's header stripping cannot protect a redirected request.
			if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
}

type bearing struct {
	token string
	next  http.RoundTripper
}

func (b bearing) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}
