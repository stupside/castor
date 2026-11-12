package mediaroute

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
)

// Publisher serves the media handlers registered by active deliveries.
type Publisher struct {
	route *url.URL
	mux   *http.ServeMux
	mu    sync.Mutex
	open  map[string]*publication
}

// NewPublisher binds published handlers to the device-reachable media route.
func NewPublisher(reach *url.URL) *Publisher {
	base := *reach
	if base.Path == "" {
		base.Path = "/"
	}
	s := &Publisher{route: base.JoinPath("media"), mux: http.NewServeMux(), open: map[string]*publication{}}
	s.mux.HandleFunc("/media/{delivery}/{path...}", s.serveDelivery)
	return s
}

// Publish registers h without opening another listener.
func (s *Publisher) Publish(ctx context.Context, h http.Handler) (output.Publication, error) {
	id := rand.Text()
	life, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p := &publication{base: s.route.JoinPath(id, "/"), handler: h, life: life, cancel: cancel}
	p.remove = func() {
		s.mu.Lock()
		delete(s.open, id)
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.open[id] = p
	s.mu.Unlock()
	return p, nil
}

type publication struct {
	base    *url.URL
	handler http.Handler
	life    context.Context
	cancel  context.CancelFunc
	remove  func()
	once    sync.Once
	active  sync.WaitGroup
	mu      sync.Mutex
}

func (p *publication) URL(path string) *url.URL { return p.base.JoinPath(path) }

func (p *publication) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A web receiver fetches by script from another origin, so whatever a device is served it may read.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	p.mu.Lock()
	if p.life.Err() != nil {
		p.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	p.active.Add(1)
	p.mu.Unlock()
	defer p.active.Done()
	ctx, cancel := context.WithCancel(p.life)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	// Context cancellation wakes readers; a deadline also wakes a blocked socket
	// writer. Join the callback before net/http takes back the ResponseWriter.
	interrupted := make(chan struct{})
	interrupt := context.AfterFunc(ctx, func() {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !interrupt() {
			<-interrupted
		}
	}()
	p.handler.ServeHTTP(w, r.WithContext(ctx))
}

func (p *publication) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.cancel()
		p.mu.Unlock()
		p.remove()
		p.active.Wait()
	})
	return nil
}
