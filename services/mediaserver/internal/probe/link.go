package probe

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Link is a found stream as castor would read it: where, and the arguments its reader opens it with.
type Link struct {
	URL  *url.URL
	Args []string
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
	slog.DebugContext(ctx, "running ffprobe", "host", l.URL.Host)
	info, reach, err := pass{
		ffprobePath: p.ffprobePath,
		budget:      p.timeout,
		inputArgs:   l.Args,
		input:       l.URL.String(),
	}.run(ctx)
	if err != nil {
		return media.ProbeInfo{}, reach, err
	}
	if info.ContentType == "" {
		slog.DebugContext(ctx, "unrecognised container; the source will be read rather than handed over",
			"host", l.URL.Host)
	}
	return info, reach, nil
}
