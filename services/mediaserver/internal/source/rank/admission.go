package rank

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// reason names the rule that decided a candidate's fate.
type reason string

const (
	reasonCastable        reason = "carried a castable program"
	reasonUnproven        reason = "unmeasurable, admitted as a last resort"
	reasonRefused         reason = "refused by the origin"
	reasonBrowserInternal reason = "a browser-internal handle: the real stream was never captured"
	reasonNoProgram       reason = "carried no video program"
	reasonTooShort        reason = "too short to be content, admitted as a last resort"
	// A header captured instead of the thing it heads; the move is casting the manifest that lists it.
	reasonFragmentHeader reason = "an MP4 header with no timeline of its own, admitted as a last resort"
)

// admissionRule is one row: the shape it recognises, and what that shape earns.
type admissionRule struct {
	reason     reason
	when       func(c measured) bool
	admit      bool
	lastResort bool
}

// admissionTable is the ordered rows plus the row that answers whatever they did not.
type admissionTable struct {
	rules []admissionRule
	total admissionRule
}

var browserInternalSchemes = []string{"blob", "filesystem"}

// browserInternal reports a URL that names bytes inside a browser.
func browserInternal(u *url.URL) bool {
	return slices.Contains(browserInternalSchemes, u.Scheme)
}

// admissions decides what castor will attempt, first match wins; Order is the contract.
var admissions = admissionTable{rules: []admissionRule{{
	// First, because no measurement can rescue it; structurally uncastable is not unproven.
	reason: reasonBrowserInternal,
	when:   func(c measured) bool { return browserInternal(c.URL) },
}, {
	// A spent signed link answers 403 to every reader alike, so nothing rescues it.
	reason: reasonRefused,
	when:   func(c measured) bool { return c.reach == media.ReachRefused },
}, {
	reason:     reasonUnproven,
	when:       func(c measured) bool { return c.Probe == nil },
	admit:      true,
	lastResort: true,
}, {
	// Measured cleanly and carries no moving picture; audio is not required.
	reason: reasonNoProgram,
	when:   func(c measured) bool { return !movingPicture(c.Probe) },
}, {
	// A known duration under a feature's is likely an ad, but a trailer or clip is real: only a title ranks above it.
	reason: reasonTooShort,
	when: func(c measured) bool {
		return source.ShorterThanContent(c.Probe.Duration)
	},
	admit:      true,
	lastResort: true,
}, {
	reason: reasonFragmentHeader,
	when: func(c measured) bool {
		return c.Probe.ContentType == media.MP4 && c.Probe.Duration == 0
	},
	admit:      true,
	lastResort: true,
}}, total: admissionRule{
	reason: reasonCastable,
	admit:  true,
}}

// admit returns the first matching row's verdict, or the total row's.
func admit(c measured) admissionRule {
	for _, rule := range admissions.rules {
		if rule.when(c) {
			return rule
		}
	}
	return admissions.total
}

// logRejection reports a dropped candidate together with the facts its reason was read from.
func logRejection(ctx context.Context, c measured, v admissionRule) {
	attrs := []any{"url", c.URL.String(), "reason", string(v.reason), "reach", c.reach}
	if probe := c.Probe; probe != nil {
		attrs = append(attrs, "video", string(probe.VideoCodec), "audio", string(probe.AudioCodec), "duration", probe.Duration)
	}
	slog.WarnContext(ctx, "candidate rejected", attrs...)
}

// tally counts rejections by the rule that made them, in table order.
func tally(rejected map[reason]int) string {
	counts := make([]string, 0, len(rejected))
	for _, rule := range admissions.rules {
		if n := rejected[rule.reason]; n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, rule.reason))
		}
	}
	return strings.Join(counts, ", ")
}

// movingPicture reports whether the selected video track carries a moving picture, not a still image.
func movingPicture(p *media.ProbeInfo) bool {
	return p.VideoCodec != "" && !stillImageCodecs[p.VideoCodec]
}

// stillImageCodecs are the ffprobe codec names observed for image tracks published as video.
var stillImageCodecs = map[media.Codec]bool{
	"png": true, "apng": true, media.CodecMJPEG: true, "jpeg": true, "jpegls": true,
	"bmp": true, "gif": true, "tiff": true, "webp": true, "ppm": true,
}
