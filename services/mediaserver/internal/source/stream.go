package source

import (
	"cmp"
	"net/http"
	"net/url"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Stream is one link a cast may read, with what capture and measurement said of it.
type Stream struct {
	URL *url.URL

	Ladder Ladder

	// Probe is a ranking summary. It does not establish the tracks a newly
	// resolved program reads; only Observation can supply those facts.
	Probe *media.ProbeInfo
	// Observation names the selected inputs and tracks behind Probe. Ranking
	// keeps the summary on the candidate; resolution reuses only matching facts.
	Observation *media.Observation

	// LastResort is a link ranking admitted without a measurement to back it.
	LastResort bool

	Headers http.Header
	// ContentType is one of media's content types, never a server's raw MIME type; "" until something names it.
	ContentType string
}

// MeasuredContentType reconciles the announced format with the probe's shared
// format names. Ranking and observation binding must use the same identity.
func (s *Stream) MeasuredContentType(info media.ProbeInfo) string {
	identified := info.ContentType
	// FFprobe reports WebM and Matroska under the same format name.
	if identified == media.MKV && (Formats{}).Named(s.URL) == media.WebM {
		identified = media.WebM
	}
	return cmp.Or(s.ContentType, identified)
}

// Inspection is a candidate's ranking summary and, when successful, the
// observation of the exact program that supplied it.
type Inspection struct {
	Info        media.ProbeInfo
	Reach       media.Reach
	Observation *media.Observation
	// ContentType is what inspection read the link as, whether or not measuring succeeded.
	ContentType string
}

// Bitrate is the rate a measurement established for this stream, 0 when none did.
func (s *Stream) Bitrate() media.Bitrate {
	if s.Probe == nil {
		return 0
	}
	return media.Bitrate(s.Probe.BitRate)
}

// minContentDuration is the shortest runtime treated as real content; pre-roll ads run well under it.
const minContentDuration = 5 * time.Minute

// ShorterThanContent reports a known runtime under a feature's, which is what an ad runs.
func ShorterThanContent(runtime time.Duration) bool {
	return runtime > 0 && runtime < minContentDuration
}
