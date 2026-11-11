package ffmpeg

import (
	"context"
	"fmt"
	"log/slog"
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

	AverageRate        bool // honors -b:v alone, no VBV ceiling
	GOPKeyframes       bool // places keyframes by -g alone, ignoring forced ones
	SingleIDROutOfBand bool // with the decoder config out of band, only its first frame is an IDR
	MediaCodecLevels   bool // reads -level:v by MediaCodec's names: 4, 4.2, m4.1
}

// Level is level as e's -level:v reads it: MediaCodec's names drop ".0", and HEVC's name its main tier.
func (e Encoder) Level(level string) string {
	if !e.MediaCodecLevels {
		return level
	}
	level = strings.TrimSuffix(level, ".0")
	if e.Codec == media.CodecHEVC {
		return "m" + level
	}
	return level
}

const vaapiRenderNode = "/dev/dri/renderD128"

// pix_fmt is fixed on CPU encoders; VA-API converts on the GPU instead.
var (
	// veryfast keeps the software encoders ahead of realtime in the live pipeline.
	softwareFlags     = []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}
	videotoolboxFlags = []string{"-pix_fmt", "yuv420p", "-g", "600"}
	vaapiFilters      = []string{"format=nv12", "hwupload"}
	mediacodecFlags   = []string{"-pix_fmt", "nv12", "-bitrate_mode", "vbr"}
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

	h264MediaCodec = Encoder{Name: "h264_mediacodec", Codec: media.CodecH264, Hardware: true, Flags: mediacodecFlags, AverageRate: true, GOPKeyframes: true, SingleIDROutOfBand: true, MediaCodecLevels: true}
	hevcMediaCodec = Encoder{Name: "hevc_mediacodec", Codec: media.CodecHEVC, Hardware: true, Flags: mediacodecFlags, AverageRate: true, GOPKeyframes: true, SingleIDROutOfBand: true, MediaCodecLevels: true}

	h264VAAPI = vaapiEncoder("h264_vaapi", media.CodecH264, vaapiRenderNode)
	hevcVAAPI = vaapiEncoder("hevc_vaapi", media.CodecHEVC, vaapiRenderNode)
)

// encoders is every encoder castor tries on its own, grouped by codec, hardware before software.
var encoders = []Encoder{
	h264VideoToolbox, h264NVENC, h264QSV, h264VAAPI, h264AMF, libx264,
	hevcVideoToolbox, hevcNVENC, hevcQSV, hevcVAAPI, hevcAMF, libx265,
}

// known is every encoder castor knows: those it tries on its own, and MediaCodec, which it does not probe; the Android host declares the ones its phone backs with hardware.
var known = slices.Concat(encoders, []Encoder{h264MediaCodec, hevcMediaCodec})

// Declared is a hardware encoder the host says its ffmpeg has, configured by its name and tried first.
type Declared Encoder

func (d *Declared) UnmarshalText(name []byte) error {
	i := slices.IndexFunc(known, func(e Encoder) bool { return e.Hardware && e.Name == string(name) })
	if i < 0 {
		return fmt.Errorf("%q is not a hardware encoder castor knows", name)
	}
	*d = Declared(known[i])
	return nil
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

// Encoders is the best working encoder per codec on ffmpegPath, declared ones first and hardware first unless software; each is proven once.
func Encoders(ffmpegPath string, declared []Declared) func(ctx context.Context, codec media.Codec, software bool) (Encoder, bool) {
	proven := &provenEncoders{ffmpegPath: ffmpegPath, slots: map[string]*proof{}}
	for _, d := range declared {
		proven.declared = append(proven.declared, Encoder(d))
	}
	return proven.best
}

type provenEncoders struct {
	ffmpegPath string
	declared   []Encoder

	mu    sync.Mutex
	slots map[string]*proof
}

// proof is one encoder's test, held per encoder so a slow test blocks only askers of that same encoder.
type proof struct {
	mu     sync.Mutex
	tested bool
	works  bool
}

func (p *provenEncoders) best(ctx context.Context, codec media.Codec, software bool) (Encoder, bool) {
	all := candidates(p.declared)
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
	_, err := Run(ctx, ffmpegPath, args, nil, nil)
	return err == nil
}
