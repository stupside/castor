package hls

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
)

// Handler serves retained artifacts for the publication's lifetime, independently
// of whether the producer is still writing them.
func (s *Segments) Handler() http.Handler {
	files := http.FileServerFS(s.store.root.FS())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.o.Answering(func(w *output.Answer, r *http.Request) {
		output.AllowWebReceivers(w.Header())
		s.mu.Lock()
		s.lastRequest = time.Now()
		s.mu.Unlock()
		if r.Context().Err() != nil {
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if s.runtime > 0 && r.URL.Path == "/master.m3u8" {
			s.serveMaster(w, r)
			return
		}
		if s.runtime > 0 && r.URL.Path == "/"+s.store.rootPlaylist() {
			s.serveEvent(w, r)
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

// Explicitly start an in-progress event at the beginning, rather than at its
// current live edge. Only complete fragments are in the atomically written list.
func (s *Segments) serveEvent(w http.ResponseWriter, r *http.Request) {
	body, err := s.playlist(s.store.rootPlaylist())
	if err != nil {
		http.Error(w, "the event playlist is unavailable", http.StatusServiceUnavailable)
		return
	}
	playlist := "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n" + strings.TrimPrefix(body, "#EXTM3U\n")
	s.answerPlaylist(w, r, s.store.rootPlaylist(), playlist)
}

func (s *Segments) playlist(name string) (string, error) {
	f, err := s.store.root.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	const limit = 4 << 20
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if len(body) > limit || !strings.HasPrefix(string(body), "#EXTM3U\n") {
		return "", fmt.Errorf("invalid generated playlist")
	}
	return string(body), nil
}

func (s *Segments) answerPlaylist(w http.ResponseWriter, r *http.Request, name, body string) {
	w.Header().Set("Content-Type", s.o.Format.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, time.Time{}, strings.NewReader(body))
}

// FFmpeg can omit CODECS for copied HEVC/AC3. Describe the actual init segment
// rather than allowing a Web Receiver to assume AVC/AAC for this native stream.
func (s *Segments) serveMaster(w http.ResponseWriter, r *http.Request) {
	body, err := s.playlist("master.m3u8")
	if err != nil {
		http.Error(w, "the master playlist is unavailable", http.StatusServiceUnavailable)
		return
	}
	f, err := s.store.root.Open("init.mp4")
	if err != nil {
		http.Error(w, "the media initialization is unavailable", http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 4<<20 {
		http.Error(w, "invalid media initialization", http.StatusServiceUnavailable)
		return
	}
	init, err := mp4.DecodeFile(f, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	if err != nil || init.Moov == nil {
		http.Error(w, "invalid media initialization", http.StatusServiceUnavailable)
		return
	}
	codecs, err := mediaCodecs(init.Moov)
	if err != nil {
		http.Error(w, "invalid media codecs", http.StatusServiceUnavailable)
		return
	}
	s.answerPlaylist(w, r, "master.m3u8", eventMaster(body, codecs))
}

var codecAttribute = regexp.MustCompile(`CODECS="[^"]*"`)

func eventMaster(body, codecs string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		attribute := "CODECS=" + fmt.Sprintf("%q", codecs)
		if codecAttribute.MatchString(line) {
			lines[i] = codecAttribute.ReplaceAllString(line, attribute)
		} else {
			lines[i] = line + "," + attribute
		}
	}
	return strings.Join(lines, "\n")
}
