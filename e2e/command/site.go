package command

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/origin"
)

// site serves the page on every route it does not mount a decoy on, and records which routes a browser asked for.
type site struct {
	url   string
	mu    sync.Mutex
	paths []string
}

func serveSite(t *testing.T, src *origin.Origin, p Page) *site {
	s := &site{}
	mux := http.NewServeMux()
	markup, script := p.Offer.Mount(t, mux, src)
	scripts := []template.JS{script}
	for _, d := range p.Decoys {
		scripts = append(scripts, template.JS(d.Mount(mux, src)))
	}
	page := struct {
		Markup  template.HTML
		Scripts []template.JS
	}{markup, scripts}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = document.Execute(w, page)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	s.url = server.URL
	return s
}

// visited holds that castor's browser opened the page at the route the command names.
type visited struct {
	site  *site
	route string
}

func (visited) Name() string { return "page" }

func (v visited) Judge(judge.Evidence) []string {
	v.site.mu.Lock()
	defer v.site.mu.Unlock()
	if slices.Contains(v.site.paths, v.route) {
		return nil
	}
	return []string{fmt.Sprintf("the browser opened %v, want %s", strings.Join(v.site.paths, ", "), v.route)}
}
