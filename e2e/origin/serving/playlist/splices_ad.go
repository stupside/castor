package playlist

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
	"github.com/stupside/castor/e2e/strategy"
)

// SplicesAd builds a server-side ad pod stitched into the media playlist from another host, as in `splices-ad: {after: 4, segments: 3, height: 720}`.
type SplicesAd struct{}

func (SplicesAd) Name() string { return "splices-ad" }

func (SplicesAd) Build(settings yaml.Node) (origin.Behaviour, error) {
	var s splice
	if err := strategy.Decode(settings, &s); err != nil {
		return nil, fmt.Errorf("splices-ad: %w", err)
	}
	if s.After < 1 || s.Segments < 1 || s.Height < 2 {
		return nil, errors.New("splices-ad: want after >= 1, segments >= 1 and height >= 2")
	}
	creative, err := encodeCreative(s.Segments, s.Height)
	if err != nil {
		return nil, fmt.Errorf("splices-ad: %w", err)
	}
	s.creative = creative
	return s, nil
}

type splice struct {
	After    int `yaml:"after"`
	Segments int `yaml:"segments"`
	Height   int `yaml:"height"`

	creative creative
}

// creative is the ad in each framing a content playlist can splice it among: TS, or fMP4 behind its own init.
type creative struct {
	ts, fmp4 [][]byte
	init     []byte
}

func (splice) Name() string { return "splices-ad" }

func (s splice) Wrap(next http.Handler, p origin.Published) http.Handler {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		if path.Dir(r.URL.Path) == "/ad" && name == "init.mp4" {
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(s.creative.init)
			return
		}
		pieces, mime := s.creative.ts, "video/mp2t"
		if path.Ext(name) == ".m4s" {
			pieces, mime = s.creative.fmp4, "video/mp4"
		}
		index, err := strconv.Atoi(strings.TrimSuffix(name, path.Ext(name)))
		if path.Dir(r.URL.Path) != "/ad" || err != nil || index < 0 || index >= len(pieces) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write(pieces[index])
	}))
	go func() {
		<-p.Closing
		cdn.Close()
	}()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path.Ext(r.URL.Path) != ".m3u8" {
			next.ServeHTTP(w, r)
			return
		}
		serving.Rewrite(next, w, r, func(playlist []byte) ([]byte, bool, error) {
			if !strings.Contains(string(playlist), "#EXTINF") {
				return playlist, false, nil
			}
			if p.SegmentExt != ".ts" && p.SegmentExt != ".m4s" {
				return nil, false, fmt.Errorf("splices-ad: cannot splice a creative among %s segments", p.SegmentExt)
			}
			return s.spliced(string(playlist), cdn.URL, p.SegmentExt), true, nil
		})
	})
}

// spliced replaces the content entries the pod covers with the pod, fenced by discontinuities, so the length is unchanged.
func (s splice) spliced(playlist, cdn, ext string) []byte {
	var out strings.Builder
	// An fMP4 pod brings its own init, and the content's must be named again once the pod ends.
	var adInit, contentInit string
	if ext == ".m4s" {
		adInit = fmt.Sprintf("#EXT-X-MAP:URI=%q\n", cdn+"/ad/init.mp4")
		for line := range strings.Lines(playlist) {
			if strings.HasPrefix(line, "#EXT-X-MAP") {
				contentInit = line
				break
			}
		}
	}
	entry := 0
	for line := range serving.Lines(playlist) {
		if !line.Entry() {
			out.WriteString(line.Text)
			continue
		}
		if entry == s.After {
			out.WriteString("#EXT-X-DISCONTINUITY\n")
			out.WriteString(adInit)
			for n := range s.Segments {
				fmt.Fprintf(&out, "#EXTINF:1.000000,\n%s/ad/%03d%s?aid=e2e\n", cdn, n, ext)
			}
			out.WriteString("#EXT-X-DISCONTINUITY\n")
			out.WriteString(contentInit)
		}
		if entry < s.After || entry >= s.After+s.Segments {
			out.WriteString(line.Text)
			out.WriteString(line.URI)
		}
		entry++
	}
	return []byte(out.String())
}

// encodeCreative encodes the ad once per framing: TS, and fMP4 behind its own init.
func encodeCreative(segments, height int) (creative, error) {
	dir, err := os.MkdirTemp("", "castor-e2e-ad-")
	if err != nil {
		return creative{}, err
	}
	defer os.RemoveAll(dir)
	encode := func(ext string, framing ...string) ([][]byte, error) {
		mux := slices.Concat([]string{"-f", "hls", "-hls_time", "1", "-hls_list_size", "0", "-hls_playlist_type", "vod"}, framing,
			[]string{"-hls_segment_filename", filepath.Join(dir, "ad_%03d"+ext), filepath.Join(dir, "ad"+ext+".m3u8")})
		if err := origin.Creative(segments, height, mux...); err != nil {
			return nil, err
		}
		pieces := make([][]byte, segments)
		for n := range segments {
			if pieces[n], err = os.ReadFile(filepath.Join(dir, fmt.Sprintf("ad_%03d%s", n, ext))); err != nil {
				return nil, err
			}
		}
		return pieces, nil
	}
	var c creative
	if c.ts, err = encode(".ts"); err != nil {
		return creative{}, err
	}
	if c.fmp4, err = encode(".m4s", "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "ad_init.mp4"); err != nil {
		return creative{}, err
	}
	if c.init, err = os.ReadFile(filepath.Join(dir, "ad_init.mp4")); err != nil {
		return creative{}, err
	}
	return c, nil
}
