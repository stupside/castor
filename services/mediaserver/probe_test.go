package mediaserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"sync/atomic"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/follow"
	"github.com/stupside/castor/services/mediaserver/internal/source/rank"
	"github.com/stupside/castor/services/mediaserver/internal/source/web"
)

func TestWrappedSegmentsAreMeasuredAndReadAsVideo(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(err)
		}
	}
	ts, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=160x96:r=25", "-t", "1", "-c:v", "mpeg2video", "-f", "mpegts", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	gz := gzip.NewWriter(&zipped)
	if _, err := gz.Write(ts); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	rgb := append([]byte("TIKTIKPX"), binary.BigEndian.AppendUint32(nil, uint32(zipped.Len()))...)
	rgb = append(rgb, zipped.Bytes()...)
	img := image.NewNRGBA(image.Rect(0, 0, 16, (len(rgb)+47)/48))
	for i, v := range rgb {
		img.Pix[i/3*4+i%3] = v
	}
	var wrapped bytes.Buffer
	if err := png.Encode(&wrapped, img); err != nil {
		t.Fatal(err)
	}
	var ordinary bytes.Buffer
	if err := png.Encode(&ordinary, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	var wrappedReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100000,RESOLUTION=160x96\nvideo.m3u8\n"))
		case "/opaque", "/playlist.mp4":
			if r.Header.Get("Authorization") != "Bearer fixture" {
				http.Error(w, "missing auth", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nsegment.png\n#EXT-X-ENDLIST\n"))
		case "/plain.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nplain.ts\n#EXT-X-ENDLIST\n"))
		case "/mixed.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nplain.ts\n#EXTINF:1,\nsegment.png\n#EXT-X-ENDLIST\n"))
		case "/plain.ts":
			_, _ = w.Write(ts)
		case "/hidden.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nsegment.ts\n#EXT-X-ENDLIST\n"))
		case "/video.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nsegment.png\n#EXT-X-ENDLIST\n"))
		case "/segment.png", "/segment.ts":
			if r.URL.Path == "/segment.png" {
				wrappedReads.Add(1)
			}
			_, _ = w.Write(wrapped.Bytes())
		case "/image.png":
			_, _ = w.Write(ordinary.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := web.New(fetchTimeout)
	resolver := source.NewResolver(client, 0, nil, formats)
	timelines := follow.New(client, formats, follow.Repackager("ffmpeg"))
	for _, path := range []string{"/master.m3u8", "/video.m3u8", "/hidden.m3u8", "/opaque", "/plain.m3u8", "/mixed.m3u8"} {
		t.Run(path, func(t *testing.T) {
			u, _ := url.Parse(server.URL + path)
			stream := &source.Stream{URL: u, ContentType: media.HLS}
			if path == "/master.m3u8" || path == "/opaque" {
				stream.ContentType = ""
			}
			if path == "/opaque" {
				stream.Headers = http.Header{"Authorization": {"Bearer fixture"}}
			}
			p := candidateProbe{stream: stream, client: client, resolver: resolver, timelines: timelines, binary: probe.FFprobe("ffprobe")}
			inspection, err := p.Inspect(t.Context())
			info, reach := inspection.Info, inspection.Reach
			if err != nil || reach != media.ReachOpened || info.VideoCodec != "mpeg2video" || info.VideoHeight != 96 {
				t.Fatalf("probe: %+v, reach=%v, err=%v", info, reach, err)
			}
			if path != "/mixed.m3u8" {
				wantWrapped := path != "/plain.m3u8"
				if info.SourceUnwrapped != wantWrapped {
					t.Fatalf("observed wrapped source = %t, want %t", info.SourceUnwrapped, wantWrapped)
				}
			}
			stream.Probe = &info
			stream.Observation = inspection.Observation
			stream.ContentType = info.ContentType
			resolved, err := resolver.Resolve(t.Context(), stream, source.Rendition{})
			if err != nil {
				t.Fatal(err)
			}
			if path == "/plain.m3u8" || path == "/hidden.m3u8" {
				measured, ok := resolved.Program.Measurement()
				if !ok || measured.SourceUnwrapped != info.SourceUnwrapped {
					t.Fatalf("source envelope lost during resolution: %+v, measured=%t", measured, ok)
				}
			}
			if path == "/mixed.m3u8" && resolved.Program.SelfFetchable() {
				t.Fatal("playlist with a later wrapped segment was handed directly to a receiver")
			}
			program, stop, err := timelines.Republish(t.Context(), resolved.Program)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stop(); err != nil {
					t.Error(err)
				}
			}()
			input, _ := program.PrimaryInput()
			args := append([]string{"-v", "error"}, formats.InputArgs(input.ContentType, 0)...)
			args = append(args, "-i", input.URL.String(), "-f", "null", "-")
			cmd := exec.CommandContext(t.Context(), "ffmpeg", args...)
			before := wrappedReads.Load()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("playback: %v: %s", err, out)
			}
			if path == "/mixed.m3u8" && wrappedReads.Load() == before {
				t.Fatal("playback never read the later wrapped segment")
			}
		})
	}
	for _, tc := range []struct{ path, mime string }{
		{"/opaque", "application/vnd.apple.mpegurl"},
		{"/opaque", "application/x-mpegurl"},
		{"/opaque", "application/vnd.apple.mpegurl; charset=utf-8"},
		// An unrecognized MIME type is no evidence: the playlist body decides.
		{"/opaque", "text/plain"},
		// A recognized MIME type outranks a misleading extension.
		{"/playlist.mp4", "application/vnd.apple.mpegurl"},
	} {
		t.Run(tc.path+" "+tc.mime, func(t *testing.T) {
			u, _ := url.Parse(server.URL + tc.path)
			ranker := rank.New(rank.Config{ProbeMaxConcurrency: 1}, 0, func(ctx context.Context, s *source.Stream) (source.Inspection, error) {
				return (candidateProbe{stream: s, client: client, resolver: resolver, timelines: timelines, binary: probe.FFprobe("ffprobe")}).Inspect(ctx)
			})
			measured, err := (caster{Ranker: ranker}).Measure(t.Context(), &source.Stream{URL: u, ContentType: tc.mime, Headers: http.Header{"Authorization": {"Bearer fixture"}}})
			if err != nil || measured.Probe == nil || measured.Probe.VideoHeight != 96 || measured.ContentType != media.HLS {
				t.Fatalf("HLS MIME alias was not measured as video: %+v, %v", measured, err)
			}
			resolved, err := resolver.Resolve(t.Context(), measured, source.Rendition{})
			if err != nil {
				t.Fatal(err)
			}
			if info, ok := resolved.Program.Measurement(); !ok || !info.SourceUnwrapped {
				t.Fatalf("HLS observation lost after ranking: %+v, %t", info, ok)
			}
		})
	}
	t.Run("container MIME with parameters", func(t *testing.T) {
		u, _ := url.Parse(server.URL + "/plain.ts")
		ranker := rank.New(rank.Config{ProbeMaxConcurrency: 1}, 0, func(ctx context.Context, s *source.Stream) (source.Inspection, error) {
			return (candidateProbe{stream: s, client: client, resolver: resolver, timelines: timelines, binary: probe.FFprobe("ffprobe")}).Inspect(ctx)
		})
		measured, err := (caster{Ranker: ranker}).Measure(t.Context(), &source.Stream{URL: u, ContentType: "Video/MP2T; charset=binary"})
		if err != nil || measured.Probe == nil || measured.ContentType != media.MPEGTS {
			t.Fatalf("container MIME was not measured as its content type: %+v, %v", measured, err)
		}
		resolved, err := resolver.Resolve(t.Context(), measured, source.Rendition{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := resolved.Program.Measurement(); !ok {
			t.Fatal("observation bound under another identity than ranking kept")
		}
	})
	t.Run("static image rejected", func(t *testing.T) {
		u, _ := url.Parse(server.URL + "/image.png")
		ranker := rank.New(rank.Config{ProbeMaxConcurrency: 1}, 0, func(ctx context.Context, s *source.Stream) (source.Inspection, error) {
			return (candidateProbe{stream: s, client: client, resolver: resolver, timelines: timelines, binary: probe.FFprobe("ffprobe")}).Inspect(ctx)
		})
		if _, err := ranker.Rank(t.Context(), []*source.Stream{{URL: u, ContentType: "image/png"}}); err == nil {
			t.Fatal("static image admitted")
		}
	})
}
