package viewer

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/receiver"
	"github.com/stupside/castor/e2e/strategy"
)

// Takes builds a viewer whose link is capped for a while: `takes: {rate: 2M, burst: 32M, for: 25s}`.
type Takes struct{}

func (Takes) Name() string { return "takes" }

func (Takes) Build(settings yaml.Node) (receiver.Viewer, error) {
	var in struct {
		Rate  string        `yaml:"rate"`
		Burst string        `yaml:"burst"`
		For   time.Duration `yaml:"for"`
	}
	if err := strategy.Decode(settings, &in); err != nil {
		return nil, err
	}
	rate, err := strategy.Rate(in.Rate)
	if err != nil {
		return nil, fmt.Errorf("takes.rate: %w", err)
	}
	burst, err := strategy.Rate(in.Burst)
	if err != nil {
		return nil, fmt.Errorf("takes.burst: %w", err)
	}
	if in.For <= 0 {
		return nil, fmt.Errorf("takes.for: want a positive duration such as 40s")
	}
	return limited{rate: rate, burst: burst / 8, lasts: in.For}, nil
}

type limited struct {
	// rate is bits a second; burst is the bytes taken before it applies; lasts is how long the link stays so.
	rate, burst int
	lasts       time.Duration
}

func (limited) Name() string { return "takes" }

func (limited) Watch(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
func (limited) Stopped(context.Context) bool { return false }
func (limited) WillStop() bool               { return false }
func (limited) Realtime() bool               { return false }

func (l limited) Pace(r io.Reader) io.Reader { return &rationed{r: r, limit: l} }

// rationed holds reads to the rate after the burst.
type rationed struct {
	r     io.Reader
	limit limited
	start time.Time
	read  int64
}

func (p *rationed) Read(b []byte) (int, error) {
	n, err := p.r.Read(b[:min(len(b), 16<<10)])
	if p.start.IsZero() {
		p.start = time.Now()
	}
	p.read += int64(n)
	if time.Since(p.start) >= p.limit.lasts {
		return n, err
	}
	if over := p.read - int64(p.limit.burst); over > 0 {
		due := p.start.Add(time.Duration(over) * 8 * time.Second / time.Duration(p.limit.rate))
		time.Sleep(time.Until(due))
	}
	return n, err
}
