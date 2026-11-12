package hls

import (
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
)

// Handler serves retained artifacts for the publication's lifetime, independently
// of whether the producer is still writing them.
func (s *Segments) Handler() http.Handler {
	files := http.FileServerFS(s.store.root.FS())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.o.Answering(func(w *output.Answer, r *http.Request) {
		s.mu.Lock()
		s.lastRequest = time.Now()
		s.mu.Unlock()
		if r.Context().Err() != nil {
			return
		}
		if s.store.captions != nil && s.store.captions.serve(w, r) {
			return
		}
		// Go doesn't register .m3u8/.m4s; set type before ServeContent sniffs.
		if ct, ok := artifactContentType(r.URL.Path); ok {
			w.Header().Set("Content-Type", ct)
		}
		files.ServeHTTP(w, r)
		// Count from answer, not request (404 on window-rolled segments shouldn't count).
		if r.Method == http.MethodGet && w.Carried() && isMediaArtifact(strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")) {
			s.served.Add(1)
		}
	}))
	return mux
}
