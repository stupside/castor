package extract

import (
	"net/url"
	"strings"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// document is evidence from a response the browser already fetched.
// runtime is known only for a closed HLS media playlist.
type document struct {
	ladder     castorv1.Ladder
	references []*url.URL
	runtime    time.Duration
}

func inspectDocument(body string, base *url.URL) document {
	if len(body) > documentSizeLimit {
		return document{}
	}
	body = strings.TrimSpace(strings.TrimPrefix(body, "\ufeff"))
	first, _, _ := strings.Cut(body, "\n")
	if strings.TrimSpace(first) == "#EXTM3U" {
		return inspectHLS(body, base)
	}
	return inspectDASH(body, base)
}

func (d *document) addReference(ref string, base *url.URL) {
	if base == nil || ref == "" || len(ref) > 8192 || len(d.references) >= maxNamedResources {
		return
	}
	u, err := base.Parse(ref)
	if err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
		d.references = append(d.references, u)
	}
}
