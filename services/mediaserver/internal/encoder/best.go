package encoder

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Best is the best working encoder per codec on ffmpegPath, declared ones first and hardware first unless software; each is proven once.
func Best(ffmpegPath string, declared []Encoder, families ...Family) func(ctx context.Context, codec media.Codec, software bool) (Encoder, bool) {
	proven := &provenEncoders{ffmpegPath: ffmpegPath, declared: declared, families: families, slots: map[string]*proof{}}
	return proven.best
}

type provenEncoders struct {
	ffmpegPath string
	declared   []Encoder
	families   []Family

	mu    sync.Mutex
	slots map[string]*proof
}

// proof is one encoder's test, held per encoder so a slow test blocks only askers of that same encoder.
type proof struct {
	mu     sync.Mutex
	tested bool
	works  bool
}

// candidates is the declared encoders, then every family's that is not declared-only.
func (p *provenEncoders) candidates() []Encoder {
	out := slices.Clone(p.declared)
	for _, family := range p.families {
		for _, e := range family.Encoders() {
			if !e.DeclaredOnly {
				out = append(out, e)
			}
		}
	}
	return out
}

func (p *provenEncoders) best(ctx context.Context, codec media.Codec, software bool) (Encoder, bool) {
	all := p.candidates()
	passes := []bool{true, false}
	if software {
		passes = passes[1:]
	}
	for _, hardware := range passes {
		for _, e := range all {
			if e.Codec != codec || e.Hardware != hardware {
				continue
			}
			works, ran := p.available(ctx, e)
			if !works {
				continue
			}
			if ran {
				slog.InfoContext(ctx, "encoder selected", "encoder", e.Name, "codec", string(codec), "hardware", hardware)
			}
			return e, true
		}
	}
	return Encoder{}, false
}

// available reports whether e works, and whether this call ran the test encode that proved it.
func (p *provenEncoders) available(ctx context.Context, e Encoder) (works, ran bool) {
	key := strings.Join(slices.Concat([]string{e.Name}, e.InitArgs, e.Filters, e.Flags), "\x00")
	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		slot = &proof{}
		p.slots[key] = slot
	}
	p.mu.Unlock()

	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.tested {
		return slot.works, false
	}
	works = testEncode(ctx, p.ffmpegPath, e)
	// A cancelled test proves nothing, and remembering it would disable working hardware.
	if ctx.Err() == nil {
		slot.tested, slot.works = true, works
	}
	if !works {
		slog.InfoContext(ctx, "encoder unavailable", "encoder", e.Name, "hardware", e.Hardware)
	}
	return works, true
}

// testEncode encodes one tenth of a second with e's own device setup and filters.
func testEncode(ctx context.Context, ffmpegPath string, e Encoder) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := append([]string{"-hide_banner"}, e.InitArgs...)
	args = append(args, "-f", "lavfi", "-i", "testsrc2=size=256x144:rate=25:duration=0.1")
	if len(e.Filters) > 0 {
		args = append(args, "-vf", strings.Join(e.Filters, ","))
	}
	args = append(args, "-c:v", e.Name)
	args = append(args, e.Flags...)
	args = append(args, "-f", "null", "-")
	_, err := ffmpeg.Run(ctx, ffmpegPath, args, nil, nil)
	return err == nil
}
