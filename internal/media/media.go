package media

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	MP4    = "video/mp4"
	MKV    = "video/x-matroska"
	WebM   = "video/webm"
	AVI    = "video/x-msvideo"
	MOV    = "video/quicktime"
	HLS    = "application/x-mpegURL"
	MPEGTS = "video/mp2t"
)

// HLS output filenames, shared by the muxer (ffmpeg's hls muxer writes them
// into its working directory) and the HLS server (which serves that directory).
const (
	HLSPlaylistName   = "stream.m3u8"
	HLSInitName       = "init.mp4"
	HLSSegmentPattern = "seg_%05d.m4s"
)

// HLSInputArgs contains ffmpeg/ffprobe flags that relax extension checks
// for HLS playlists and DASH manifests.
var HLSInputArgs = []string{
	"-allowed_extensions", "ALL",
	"-allowed_segment_extensions", "ALL",
	"-extension_picky", "0",
	"-seg_format_options", "extension_picky=0",
}

// Stream is one castable program: where castor reads it from, and what it needs
// to read it. A program is usually one URL carrying both tracks, but a source is
// free to publish them separately (an HLS master with an audio rendition group),
// in which case it takes two reads to get a whole program.
type Stream struct {
	URL *url.URL

	// AudioURL is the companion audio rendition, set when the source publishes
	// its tracks separately: URL then carries video alone and the two are read
	// together as one program. nil is the ordinary case, audio muxed into URL.
	AudioURL *url.URL

	Headers     http.Header
	Bandwidth   int64
	ContentType string
	Live        bool
}

// Demuxed reports whether the program's tracks live at separate URLs, so a
// reader needs both.
func (s *Stream) Demuxed() bool { return s.AudioURL != nil }

// SelfFetchable reports whether a renderer handed nothing but this stream's URL
// can pull the whole program itself. A pass-through gives the device that URL
// and nothing else, which rules out two shapes:
//
//   - a header-gated source. None of the request headers castor captured while
//     extracting (Referer, Origin, Cookie, User-Agent) travel with the URL, and
//     castor cannot know which of them the origin gates on, so a URL it only ever
//     fetched with headers is not proven fetchable without them.
//   - a demuxed program. One URL is one rendition, so the renderer would play
//     the video and none of the audio.
//
// Either way the device fails where castor succeeds, and silently: it accepts the
// load and then sits idle, or plays in silence. Such a source is served locally
// instead, castor reading what it needs and giving the renderer a single LAN URL.
// A header-free, self-contained source (the direct URL a user casts by hand)
// passes through.
func (s *Stream) SelfFetchable() bool { return len(s.Headers) == 0 && !s.Demuxed() }

// StreamInfo holds metadata returned by ffprobe for a stream.
type StreamInfo struct {
	BitRate     int64
	Duration    time.Duration
	ContentType string
	HasVideo    bool
	HasAudio    bool
	// VideoHeight is the display height of the real video track, 0 if unknown.
	// For an HLS master this is only whichever variant ffprobe chose, not the
	// master's full range, so it is not a reliable ceiling for a master.
	VideoHeight int
}

// Playable reports whether the stream carries castable media — a real video
// track plus audio. Decoy playlists (an image-only "video" track, or no audio)
// probe cleanly but cannot be remuxed, so they are not playable.
func (s StreamInfo) Playable() bool { return s.HasVideo && s.HasAudio }

// Live reports whether ffprobe could determine no duration — genuinely live
// sources have none (no endlist), and unparseable duration is safest treated
// the same: pacing such sources at realtime costs nothing on VOD while 2x
// with a wire-speed burst trips rate limits on proxy CDNs.
func (s StreamInfo) Live() bool { return s.Duration == 0 }

// DeliveryKind is how castor's local HTTP server hands a produced stream to the
// renderer, and the single fact the delivery driver keys the serving mechanism
// on. It is carried as data on FormatInfo, so a producible format's delivery is
// declared alongside it rather than decided by a content-type conditional.
type DeliveryKind int

const (
	// DeliverStream is one growing output the replay server fronts, handing every
	// client the stream from byte 0 (MPEG-TS, fragmented mp4).
	DeliverStream DeliveryKind = iota
	// DeliverSegmented is a live playlist plus rolling segments in a directory the
	// HLS server fronts.
	DeliverSegmented
)

// FormatInfo describes a container castor can produce: the MIME type the device
// is told it is fetching, the file extension it carries, the ffmpeg muxer (-f)
// that writes it, and how it is delivered.
type FormatInfo struct {
	ContentType string
	Extension   string
	Muxer       string
	Delivery    DeliveryKind
}

// formatRegistry is the vocabulary of containers castor can produce, keyed by
// content type. It is the single source of truth the media helpers and the
// delivery driver read; adding a producible format is one row here (no code path
// enumerates formats or deliveries elsewhere).
var formatRegistry = map[string]FormatInfo{
	MPEGTS: {ContentType: MPEGTS, Extension: ".ts", Muxer: "mpegts", Delivery: DeliverStream},
	MP4:    {ContentType: MP4, Extension: ".mp4", Muxer: "mp4", Delivery: DeliverStream},
	HLS:    {ContentType: HLS, Extension: ".m3u8", Muxer: "hls", Delivery: DeliverSegmented},
}

// FormatForContentType returns the FormatInfo for a content type, or ok=false if
// castor cannot produce it. Callers read the muxer, extension, and delivery off
// the returned FormatInfo.
func FormatForContentType(ct string) (FormatInfo, bool) {
	f, ok := formatRegistry[ct]
	return f, ok
}

var extensionMap = map[string]string{
	".mp4":  MP4,
	".mkv":  MKV,
	".webm": WebM,
	".avi":  AVI,
	".mov":  MOV,
	".m3u8": HLS,
}

// DetectFromExtension returns a content type based on the URL's file extension,
// or empty string if unrecognized.
func DetectFromExtension(u *url.URL) string {
	return extensionMap[strings.ToLower(path.Ext(u.Path))]
}

var mimeContentTypes = map[string]string{
	"video/mp4":                     MP4,
	"video/webm":                    WebM,
	"video/x-matroska":              MKV,
	"audio/mpegurl":                 HLS,
	"audio/x-mpegurl":               HLS,
	"application/x-mpegurl":         HLS,
	"application/vnd.apple.mpegurl": HLS,
}

// DetectFromMIME returns a content type based on a confirmed MIME type,
// or empty string if unrecognized.
func DetectFromMIME(mime string) string {
	return mimeContentTypes[strings.ToLower(mime)]
}

// HeaderArgs renders h as the ffmpeg/ffprobe -headers flag pair, or nil when h is
// empty. ffprobe and ffmpeg both want every request header in one CRLF-joined blob.
func HeaderArgs(h http.Header) []string {
	if len(h) == 0 {
		return nil
	}
	var b strings.Builder
	for key, values := range h {
		for _, v := range values {
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	return []string{"-headers", b.String()}
}

// NormalizeStreamHeaders returns a copy of browser-captured headers ready to
// replay to the puller. It drops headers that break a re-issued fetch and, when
// a Referer is present without an Origin, derives the Origin from it: a
// cross-origin browser GET sends only a Referer, but CDNs commonly gate segment
// delivery on Origin too, and the derived pair is what a site's own player proxy
// sends. The input is not mutated.
func NormalizeStreamHeaders(h http.Header) http.Header {
	out := h.Clone()
	if out == nil {
		return nil
	}
	// A stale Range fetches a byte slice instead of the whole resource; a
	// br/zstd Accept-Encoding yields a body ffmpeg can't decode ("Invalid data
	// found when processing input").
	out.Del("Range")
	out.Del("Accept-Encoding")
	if out.Get("Origin") == "" {
		if origin := originOf(out.Get("Referer")); origin != "" {
			out.Set("Origin", origin)
		}
	}
	return out
}

// originOf returns the scheme://host origin of an absolute URL, or "" if s is
// not one.
func originOf(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

// FormatToContentType maps an ffprobe format_name to a content type.
func FormatToContentType(format string) (string, error) {
	for f := range strings.SplitSeq(format, ",") {
		switch strings.TrimSpace(f) {
		case "hls", "applehttp":
			return HLS, nil
		case "mp4":
			return MP4, nil
		case "matroska":
			return MKV, nil
		case "webm":
			return WebM, nil
		case "avi":
			return AVI, nil
		}
	}
	return "", fmt.Errorf("unknown format: %s", format)
}
