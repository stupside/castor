package probe

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Link is a found stream as castor would read it: where, with which headers, and what it says it is.
type Link struct {
	URL     *url.URL
	Headers http.Header
	// InputArgs is how the link's format opens it; nil reads it leniently.
	InputArgs []string
}

// Link binds a found stream to this ffprobe, measured within timeout.
func (bin FFprobe) Link(l Link, timeout time.Duration) media.Prober {
	return linkProber{ffprobePath: string(bin), timeout: timeout, link: l}
}

type linkProber struct {
	ffprobePath string
	timeout     time.Duration
	link        Link
}

func (p linkProber) Probe(ctx context.Context) (media.ProbeInfo, media.Reach, error) {
	l := p.link
	args := ffmpeg.HeaderArgs(l.Headers)
	if l.InputArgs != nil {
		args = append(args, l.InputArgs...)
	} else {
		args = append(args, ffmpeg.HLSTolerances(0)...)
	}

	slog.DebugContext(ctx, "running ffprobe", "url", l.URL.String(), "header_count", len(l.Headers))

	info, reach, err := pass{
		ffprobePath: p.ffprobePath,
		budget:      p.timeout,
		inputArgs:   args,
		input:       l.URL.String(),
	}.run(ctx)
	if err != nil {
		return media.ProbeInfo{}, reach, err
	}
	if info.ContentType == "" {
		slog.DebugContext(ctx, "unrecognised container; the source will be read rather than handed over",
			"url", l.URL.String())
	}
	return info, reach, nil
}
