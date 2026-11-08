package ffmpeg

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Encoder is one concrete way to produce a codec: the ffmpeg -c:v name plus its command fragments.
type Encoder struct {
	Name     string      // -c:v value, e.g. "libx264", "hevc_videotoolbox"
	Codec    media.Codec // the abstract codec produced, independent of the name
	Hardware bool        // GPU-backed: trusted only after a real test encode
	InitArgs []string    // emitted before the input: hardware device setup
	Filters  []string    // appended to the -vf chain: e.g. the GPU upload
	Flags    []string    // encoder-specific -c:v flags: preset, pix_fmt, GOP

	// GOPKeyframes ignores forced keyframes: only the -g length places them, counted in frames of the rate truncated
	// to a whole number, so a fractional rate (23.976) drifts them off the clock.
	GOPKeyframes bool
	// AverageRate honors -b:v alone: no VBV ceiling and no constant-quality target.
	AverageRate bool
	// Level spells castor's dotted level (e.g. "4.0") as -level:v expects it; nil passes it as is.
	Level func(string) string
}

const vaapiRenderNode = "/dev/dri/renderD128"

// pix_fmt is fixed on CPU encoders; VA-API converts on the GPU instead.
var (
	// veryfast keeps the software encoders ahead of realtime in the live pipeline.
	softwareFlags     = []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}
	videotoolboxFlags = []string{"-pix_fmt", "yuv420p", "-g", "600"}
	vaapiFilters      = []string{"format=nv12", "hwupload"}
)

var (
	libx264 = Encoder{Name: "libx264", Codec: media.CodecH264, Flags: softwareFlags}
	libx265 = Encoder{Name: "libx265", Codec: media.CodecHEVC, Flags: slices.Concat(softwareFlags, []string{"-x265-params", "log-level=error"})}

	// VideoToolbox can emit SEI that its FFmpeg wrapper cannot parse when it
	// merges embedded A53 captions. Drop that caption side data on re-encode
	// instead of aborting the video stream.
	h264VideoToolbox = Encoder{Name: "h264_videotoolbox", Codec: media.CodecH264, Hardware: true, Flags: slices.Concat(videotoolboxFlags, []string{"-a53cc", "0"})}
	hevcVideoToolbox = Encoder{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true, Flags: videotoolboxFlags}
	h264NVENC        = Encoder{Name: "h264_nvenc", Codec: media.CodecH264, Hardware: true, Flags: []string{"-preset", "p4", "-pix_fmt", "yuv420p"}}
	hevcNVENC        = Encoder{Name: "hevc_nvenc", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-preset", "p4", "-pix_fmt", "yuv420p"}}
	h264QSV          = Encoder{Name: "h264_qsv", Codec: media.CodecH264, Hardware: true, Flags: []string{"-preset", "veryfast", "-pix_fmt", "nv12"}}
	hevcQSV          = Encoder{Name: "hevc_qsv", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-preset", "veryfast", "-pix_fmt", "nv12"}}
	h264AMF          = Encoder{Name: "h264_amf", Codec: media.CodecH264, Hardware: true, Flags: []string{"-quality", "speed", "-pix_fmt", "nv12"}}
	hevcAMF          = Encoder{Name: "hevc_amf", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-quality", "speed", "-pix_fmt", "nv12"}}

	h264VAAPI = vaapiEncoder("h264_vaapi", media.CodecH264, vaapiRenderNode)
	hevcVAAPI = vaapiEncoder("hevc_vaapi", media.CodecHEVC, vaapiRenderNode)
)

// encoders is every encoder castor knows, grouped by codec, hardware before software.
var encoders = []Encoder{
	h264VideoToolbox, h264NVENC, h264QSV, h264VAAPI, h264AMF, libx264,
	hevcVideoToolbox, hevcNVENC, hevcQSV, hevcVAAPI, hevcAMF, libx265,
}

// declarable are encoders only a platform's own build offers, so castor tries them only where the host declares them.
var declarable = map[string]Encoder{
	"h264_mediacodec": mediacodecEncoder("h264_mediacodec", media.CodecH264, ""),
	"hevc_mediacodec": mediacodecEncoder("hevc_mediacodec", media.CodecHEVC, "m"),
}

// Declarable is every encoder name a host may declare, sorted.
func Declarable() []string {
	names := slices.Collect(maps.Keys(declarable))
	slices.Sort(names)
	return names
}

// mediacodecEncoder takes 8-bit NV12 buffers at an average rate; tier is the HEVC level prefix ("m" main), empty for H.264.
func mediacodecEncoder(name string, codec media.Codec, tier string) Encoder {
	return Encoder{
		Name: name, Codec: codec, Hardware: true,
		Flags:        []string{"-pix_fmt", "nv12", "-bitrate_mode", "vbr"},
		GOPKeyframes: true, AverageRate: true,
		// Its levels are named constants: "4.0" would parse as the number 4, which is level 1.1.
		Level: func(level string) string { return tier + strings.TrimSuffix(level, ".0") },
	}
}

// candidates is the declared encoders, then encoders plus VA-API on every other render node this host has.
func candidates(declared []Encoder) []Encoder {
	out := slices.Concat(declared, encoders)
	nodes, _ := filepath.Glob("/dev/dri/renderD*")
	for _, node := range nodes {
		if node == vaapiRenderNode {
			continue
		}
		out = append(out, vaapiEncoder("h264_vaapi", media.CodecH264, node), vaapiEncoder("hevc_vaapi", media.CodecHEVC, node))
	}
	return out
}

func vaapiEncoder(name string, codec media.Codec, node string) Encoder {
	const alias = "va"
	return Encoder{
		Name: name, Codec: codec, Hardware: true,
		InitArgs: []string{"-init_hw_device", "vaapi=" + alias + ":" + node, "-filter_hw_device", alias},
		Filters:  vaapiFilters,
	}
}

// Encoders is the best working encoder per codec on ffmpegPath, hardware first; each encoder is proven once by the value it returns.
func Encoders(ffmpegPath string) func(context.Context, media.Codec) (Encoder, bool) {
	c, _ := NewCatalog(ffmpegPath, nil)
	return c.Best
}

// Catalog is the encoders proven on one ffmpeg binary, and those retired after failing a cast.
type Catalog struct{ proven *provenEncoders }

// NewCatalog proves encoders on ffmpegPath, trying the declared ones, which only a platform's own build offers, before the rest.
func NewCatalog(ffmpegPath string, declared []string) (*Catalog, error) {
	p := &provenEncoders{ffmpegPath: ffmpegPath, slots: map[string]*proof{}, retired: map[string]bool{}}
	for _, name := range declared {
		e, ok := declarable[name]
		if !ok {
			return nil, fmt.Errorf("unknown hardware encoder %q: castor knows %s", name, strings.Join(Declarable(), ", "))
		}
		p.declared = append(p.declared, e)
	}
	return &Catalog{proven: p}, nil
}

// Best is the best working encoder for codec, hardware first.
func (c *Catalog) Best(ctx context.Context, codec media.Codec) (Encoder, bool) {
	return c.proven.best(ctx, codec, []bool{true, false})
}

// Software is the best working encoder for codec that needs no hardware.
func (c *Catalog) Software(ctx context.Context, codec media.Codec) (Encoder, bool) {
	return c.proven.best(ctx, codec, []bool{false})
}

// Retire stops offering the encoder named name for as long as this catalog lives.
func (c *Catalog) Retire(name string) {
	c.proven.mu.Lock()
	defer c.proven.mu.Unlock()
	if !c.proven.retired[name] {
		slog.Warn("encoder retired after failing a cast", "encoder", name)
	}
	c.proven.retired[name] = true
}

type provenEncoders struct {
	ffmpegPath string
	declared   []Encoder

	mu      sync.Mutex
	slots   map[string]*proof
	retired map[string]bool
}

// proof is one encoder's test, held per encoder so a slow test blocks only askers of that same encoder.
type proof struct {
	mu     sync.Mutex
	tested bool
	works  bool
}

func (p *provenEncoders) best(ctx context.Context, codec media.Codec, passes []bool) (Encoder, bool) {
	all := candidates(p.declared)
	for _, hardware := range passes {
		for _, e := range all {
			if e.Codec != codec || e.Hardware != hardware || p.isRetired(e.Name) {
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

func (p *provenEncoders) isRetired(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.retired[name]
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
	_, err := Run(ctx, ffmpegPath, args, nil, nil)
	return err == nil
}
