package faults

import (
	"fmt"
	"net/http"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
)

// StallsAt builds a behaviour that sends half of the nth segment requested and then holds, as in `stalls-at: 10`.
type StallsAt struct{}

func (StallsAt) Name() string { return "stalls-at" }

func (StallsAt) Build(settings yaml.Node) (origin.Behaviour, error) {
	var n int
	if err := settings.Decode(&n); err != nil || n < 1 {
		return nil, fmt.Errorf("stalls-at: want the 1-based index of the segment that stalls (%v)", err)
	}
	return &stall{at: n}, nil
}

type stall struct {
	at   int
	seen serving.Arrivals
}

func (*stall) Name() string { return "stalls-at" }

func (s *stall) Wrap(next http.Handler, p origin.Published) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.IsSegment(r) {
			next.ServeHTTP(w, r)
			return
		}
		if n, _ := s.seen.Arrive(r.URL.Path); n < s.at {
			next.ServeHTTP(w, r)
			return
		}
		body := serving.Buffered(next, w, r)
		_, _ = w.Write(body[:len(body)/2])
		_ = http.NewResponseController(w).Flush()
		p.Held(r)
	})
}
