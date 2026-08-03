package resolve

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/grafov/m3u8"
)

// hlsVariant is a single variant stream listed in an HLS master playlist.
type hlsVariant struct {
	URL       *url.URL
	Bandwidth int64
	Height    int // display height from RESOLUTION; 0 when the master omits it

	// AudioGroup is the GROUP-ID this variant plays its audio from, empty when
	// the audio is muxed into the variant's own segments. A master that names a
	// group publishes the audio as a separate rendition (see hlsMaster.Audio),
	// so the variant alone is video and silence.
	AudioGroup string

	// HasVideo reports whether the variant carries video. A master may list an
	// audio-only rendition as a variant of its own, which must never be picked
	// as the thing to cast.
	HasVideo bool
}

// hlsMaster is a parsed HLS document reduced to what selection needs: the
// variants to choose between and the audio renditions they reference. A media
// playlist reduces to a single synthetic variant, so callers treat both shapes
// uniformly.
type hlsMaster struct {
	Variants []hlsVariant

	// Audio maps an audio GROUP-ID to the rendition to play from it. Only groups
	// whose rendition has its own URI appear: a rendition without one is carried
	// inside the variant's segments and needs no separate read.
	Audio map[string]*url.URL

	// Live reports that the document is a media playlist with no
	// #EXT-X-ENDLIST, i.e. a live edge. It is always false for a master
	// playlist: the master carries no endlist signal of its own, so callers
	// must not read false as "VOD" in that case.
	Live bool
}

// AudioFor returns the rendition URL a variant reads its audio from, or nil when
// the variant's own segments carry it.
func (m hlsMaster) AudioFor(v hlsVariant) *url.URL { return m.Audio[v.AudioGroup] }

// fetchPlaylist fetches an HLS playlist and returns its body.
func fetchPlaylist(ctx context.Context, hlsTimeout time.Duration, url *url.URL, headers http.Header) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url.String(), nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	maps.Copy(req.Header, headers)

	client := &http.Client{Timeout: hlsTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching playlist: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetching playlist: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading playlist: %w", err)
	}
	return string(body), nil
}

// parsePlaylist decodes an HLS document into the variants it offers and the
// audio renditions they reference, resolving every URI against baseURL.
//
// The document is decoded by m3u8, not by us: the tag grammar is a spec surface
// (quoted attribute values carrying the same comma that separates attributes,
// I-frame variants that state their URI inline, renditions declared before or
// after the variants that use them), and hand-reading it line by line gets those
// subtly wrong in ways that surface as a cast with no sound. Decoding is
// non-strict so a real-world playlist with an unknown tag still parses. What
// stays here is only the part that is castor's policy rather than the format's:
// which variants are castable, and which rendition their audio comes from.
func parsePlaylist(body string, baseURL *url.URL) (hlsMaster, error) {
	playlist, _, err := m3u8.DecodeFrom(strings.NewReader(body), false)
	if err != nil {
		return hlsMaster{}, fmt.Errorf("decoding playlist: %w", err)
	}

	switch p := playlist.(type) {
	case *m3u8.MediaPlaylist:
		// The document is itself the thing to read, and it is the one shape that
		// reports its own liveness: no #EXT-X-ENDLIST means a live edge.
		return hlsMaster{Variants: []hlsVariant{{URL: baseURL, HasVideo: true}}, Live: !p.Closed}, nil
	case *m3u8.MasterPlaylist:
		return masterFrom(p, baseURL), nil
	default:
		return hlsMaster{}, fmt.Errorf("unsupported playlist type %T", playlist)
	}
}

// masterFrom reduces a decoded master to the variants castor can cast and the
// audio renditions they reference.
func masterFrom(playlist *m3u8.MasterPlaylist, baseURL *url.URL) hlsMaster {
	var out hlsMaster
	for _, variant := range playlist.Variants {
		if variant == nil {
			continue
		}
		// An I-frame playlist is a trick-play track: keyframes only, no audio,
		// never something to cast.
		if variant.Iframe {
			continue
		}
		variantURL, err := baseURL.Parse(variant.URI)
		if err != nil {
			continue
		}

		out.Variants = append(out.Variants, hlsVariant{
			URL:        variantURL,
			Bandwidth:  int64(variant.Bandwidth),
			Height:     resolutionHeight(variant.Resolution),
			AudioGroup: variant.Audio,
			HasVideo:   carriesVideo(variant),
		})

		// Renditions are attached to the variant that follows them, so a master
		// that declares its groups once up front hangs them all off its first
		// variant. Collecting from every variant is what makes the group map
		// whole regardless of where they were declared.
		for _, alternative := range variant.Alternatives {
			out.addRendition(alternative, baseURL)
		}
	}
	return out
}

// addRendition records an audio rendition that has a URI of its own. One without
// a URI is muxed into the variants that reference it, so there is nothing extra
// to read. Within a group, DEFAULT=YES wins over the first seen, which is the
// rendition a player picks with no preference of its own.
func (m *hlsMaster) addRendition(alternative *m3u8.Alternative, baseURL *url.URL) {
	if alternative == nil || alternative.Type != "AUDIO" || alternative.GroupId == "" || alternative.URI == "" {
		return
	}
	renditionURL, err := baseURL.Parse(alternative.URI)
	if err != nil {
		return
	}
	if _, seen := m.Audio[alternative.GroupId]; seen && !alternative.Default {
		return
	}
	if m.Audio == nil {
		m.Audio = make(map[string]*url.URL)
	}
	m.Audio[alternative.GroupId] = renditionURL
}

// carriesVideo reports whether a variant has video to cast. Positive evidence is
// a RESOLUTION or a video codec; only a CODECS list that names none rules video
// out. A variant that declares neither attribute is trusted rather than
// discarded, the same way an unknown height stays eligible for the height cap.
func carriesVideo(variant *m3u8.Variant) bool {
	return resolutionHeight(variant.Resolution) > 0 ||
		variant.Codecs == "" ||
		hasVideoCodec(variant.Codecs)
}

// resolutionHeight reads the height out of a RESOLUTION attribute ("1920x1080"),
// returning 0 when the master omits or malforms it.
func resolutionHeight(resolution string) int {
	_, height, ok := strings.Cut(resolution, "x")
	if !ok {
		return 0
	}
	h, err := strconv.Atoi(height)
	if err != nil {
		return 0
	}
	return h
}

// videoCodecPrefixes are the RFC 6381 sample-entry prefixes for video, used to
// tell a video variant from an audio-only one by its CODECS attribute.
var videoCodecPrefixes = []string{"avc1", "avc3", "hvc1", "hev1", "av01", "vp08", "vp09", "dvh1", "dvhe", "mp4v"}

// hasVideoCodec reports whether a CODECS attribute names a video codec.
func hasVideoCodec(codecs string) bool {
	for codec := range strings.SplitSeq(codecs, ",") {
		codec = strings.TrimSpace(codec)
		if slices.ContainsFunc(videoCodecPrefixes, func(prefix string) bool {
			return strings.HasPrefix(codec, prefix)
		}) {
			return true
		}
	}
	return false
}
