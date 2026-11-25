package execute

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// handedDevice is a fakeDevice that keeps what it was handed.
type handedDevice struct {
	*fakeDevice
	handed Playback
}

func (d *handedDevice) Play(ctx context.Context, p Playback) error {
	d.handed = p
	return d.fakeDevice.Play(ctx, p)
}

// lateReader is a renderer that only starts reading once Play has returned, as a TV does for the whole film.
type lateReader struct {
	*handedDevice
	read []byte
}

func (d *lateReader) Play(ctx context.Context, p Playback) error {
	d.handed = p
	return nil
}

func (d *lateReader) AwaitEnd(ctx context.Context) error {
	resp, err := http.Get(d.handed.URL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	d.read, err = io.ReadAll(resp.Body)
	return err
}

func TestAForwardedFileStaysPublishedWhileTheRendererPlaysIt(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	path := generateFixture(t, ffmpegPath, "movie.mkv", 1,
		[]string{"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "ac3", "-f", "matroska"})
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, path) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/movie")
	renderer := media.Capabilities{NativePlaybackTrial: true, Formats: []media.FormatSupport{
		{Transport: media.TransportMKV, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
	}}
	dev := &lateReader{handedDevice: &handedDevice{fakeDevice: &fakeDevice{caps: renderer}}}
	cast := realCast(dev, ffmpegPath, ffprobePath)
	cast.PreserveSource = true
	probe := &media.ProbeInfo{ContentType: media.MKV, VideoPresent: true, VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHeight: 240,
		AudioPresent: true, AudioCodec: media.CodecAC3, AudioChannels: 2, AudioSampleRate: 44100}
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	if err := castOnce(ctx, t, cast, &source.Stream{URL: u, ContentType: media.MKV, Probe: probe}); err != nil {
		t.Fatalf("cast: %v", err)
	}
	if !bytes.Equal(dev.read, file) {
		t.Fatalf("the renderer read %d bytes after Play returned, want the %d-byte file: the forward must outlive the handover", len(dev.read), len(file))
	}
}

func TestNativeForwardsTheSourceFileToARendererThatNamesItsContainer(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	path := generateFixture(t, ffmpegPath, "movie.mkv", 1,
		[]string{"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "ac3", "-ac", "6", "-f", "matroska"})
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var referer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referer = r.Header.Get("Referer")
		http.ServeFile(w, r, path)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/movie")

	// Surround AC3 is outside the renderer's family defaults: only a Native trial lets it judge the file.
	renderer := media.Capabilities{NativePlaybackTrial: true, Formats: []media.FormatSupport{
		{Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
		{Transport: media.TransportMKV, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
	}}
	for _, preserve := range []bool{true, false} {
		t.Run(map[bool]string{true: "native", false: "optimized"}[preserve], func(t *testing.T) {
			referer = ""
			dev := &handedDevice{fakeDevice: &fakeDevice{caps: renderer, drain: true}}
			cast := realCast(dev, ffmpegPath, ffprobePath)
			cast.PreserveSource = preserve
			var delivered []media.Delivery
			cast.Delivering = func(d media.Delivery) { delivered = append(delivered, d) }
			probe := &media.ProbeInfo{
				ContentType:  media.MKV,
				VideoPresent: true, VideoCodec: media.CodecH264, VideoProfile: media.ProfileHigh, VideoBitDepth: 8, VideoHeight: 240,
				AudioPresent: true, AudioCodec: media.CodecAC3, AudioChannels: 6, AudioSampleRate: 44100,
			}
			probe.VideoDolbyVision = &media.DolbyVision{Profile: 8, Level: 6, Compatibility: 1}
			stream := &source.Stream{URL: u, ContentType: media.MKV, Headers: http.Header{"Referer": {"https://player.example/"}}, Probe: probe}
			ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
			defer cancel()
			if err := castOnce(ctx, t, cast, stream); err != nil {
				t.Fatalf("cast: %v", err)
			}
			src, forwarded := dev.handed.Stream.(Source)
			want := media.Delivery{Fidelity: media.FidelityOriginalFile, DolbyVision: true}
			if !preserve {
				want.Fidelity = media.FidelityConverted
			}
			if len(delivered) != 1 || delivered[0] != want {
				t.Fatalf("the cast reported delivering %+v, want once %+v", delivered, want)
			}
			if !preserve {
				if forwarded || bytes.Equal(dev.served, file) {
					t.Fatal("outside Native, tracks the renderer never named must be converted, not forwarded")
				}
				return
			}
			if !forwarded || src.ContentType != media.MKV {
				t.Fatalf("handed %#v, want the source itself as Matroska", dev.handed.Stream)
			}
			if !bytes.Equal(dev.served, file) {
				t.Fatalf("the renderer read %d bytes that differ from the %d-byte source file", len(dev.served), len(file))
			}
			if referer != "https://player.example/" {
				t.Errorf("the origin saw Referer %q: castor must fetch with the source's own headers", referer)
			}
		})
	}
}

type redirectReader struct {
	*handedDevice
	origin string
}

func (d *redirectReader) AwaitEnd(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.handed.URL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=8-15")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != d.origin {
		return fmt.Errorf("got %s with Location %q", resp.Status, resp.Header.Get("Location"))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(body) != 0 {
		return fmt.Errorf("redirect carried %d bytes", len(body))
	}
	return nil
}

func TestRefusedOriginalAndRedirectBothReachTheDeviceAsDirectSources(t *testing.T) {
	origin := &url.URL{Scheme: "https", Host: "origin.example", Path: "/video", RawQuery: "part=one%2Btwo"}
	probe := &media.ProbeInfo{VideoPresent: true, VideoCodec: media.CodecH264, VideoHeight: 240, VideoBitDepth: 8, AudioPresent: true, AudioCodec: media.CodecAAC, AudioChannels: 2}
	program := programFromStream(t, &source.Stream{URL: origin, ContentType: media.MP4, Probe: probe})
	caps := chromecastLike(media.MP4)
	caps.SelfFetch = false
	refusal := errors.New("renderer refused the origin")
	dev := &redirectReader{handedDevice: &handedDevice{fakeDevice: &fakeDevice{caps: caps, refuse: refusal}}, origin: origin.String()}
	cast := Cast{Device: dev, PreserveSource: true, Publisher: loopback{}}
	attempt := recovery.Attempt{Try: 1, Program: program, Delivery: compose.DeliveryPreferDirect, Fetch: sourceFetchPlan(t, program, testReadDeadline)}
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	out := cast.Run(ctx, attempt)
	if !errors.Is(out.Err, refusal) || !out.Evidence.Direct || dev.handed.URL.String() != origin.String() {
		t.Fatalf("direct origin: got %v, evidence %+v, URL %v", out.Err, out.Evidence, dev.handed.URL)
	}
	dev.refuse = nil
	attempt.Try++
	attempt.Delivery = compose.DeliveryRedirect
	if out := cast.Run(ctx, attempt); out.Err != nil {
		t.Fatal(out.Err)
	}
	if src, ok := dev.handed.Stream.(Source); !ok || !src.Direct || dev.handed.URL.Host == origin.Host {
		t.Fatalf("redirect handoff: %+v", dev.handed)
	}
}
