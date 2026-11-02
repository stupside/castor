package encoding

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// scaleFilter caps encode height while keeping aspect ratio and even dimensions.
func scaleFilter(maxHeight media.HeightCap) string {
	if maxHeight <= 0 {
		return ""
	}
	// -2 makes even width; round ceiling and odd height down (yuv420 rejects odd).
	evenCap := int(maxHeight) &^ 1
	return fmt.Sprintf("scale=-2:'min(%d,max(2,trunc(ih/2)*2))'", evenCap)
}

// toneMapFilter bounds the picture in the first color conversion. Expanding an entire 4K
// frame to floating-point RGB and tone-mapping it before an HD downscale wastes most of
// the work, and can starve a live encode even when its source arrives fast enough.
func toneMapFilter(venc codec.VideoEncode) string {
	var size string
	if venc.MaxWidth > 0 && venc.MaxHeight > 0 {
		ratio := fmt.Sprintf("min(1,min(%d/iw,%d/ih))", venc.MaxWidth, int(venc.MaxHeight)&^1)
		size = fmt.Sprintf("w='max(2,trunc((iw*%s+0.5)/2)*2)':h='max(2,trunc((ih*%s+0.5)/2)*2)':", ratio, ratio)
	} else if venc.MaxHeight > 0 {
		size = fmt.Sprintf("w=-2:h='min(%d,max(2,trunc(ih/2)*2))':", int(venc.MaxHeight)&^1)
	}
	return "zscale=" + size + "t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
}

// videoFilters is a re-encode's -vf chain, in the order the frames pass through it.
func videoFilters(venc codec.VideoEncode) []string {
	var filters []string
	if venc.Deinterlace {
		filters = append(filters, "bwdif=mode=send_frame")
	}
	if venc.MaxFrameRate > 0 {
		filters = append(filters, fmt.Sprintf("fps='min(source_fps,%s)'", strconv.FormatFloat(venc.MaxFrameRate, 'f', -1, 64)))
	}
	if venc.ToneMap {
		filters = append(filters, toneMapFilter(venc))
	} else if venc.MaxWidth > 0 && venc.MaxHeight > 0 {
		filters = append(filters, fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease:force_divisible_by=2", venc.MaxWidth, int(venc.MaxHeight)&^1))
	} else if f := scaleFilter(venc.MaxHeight); f != "" {
		filters = append(filters, f)
	}
	if venc.SubtitleTextFile != "" {
		filters = append(filters, drawtextFilter(venc.SubtitleTextFile))
	}
	return append(filters, venc.Encoder.Filters...)
}

func videoFilterArgs(video codec.Track[codec.VideoEncode]) []string {
	venc, ok := video.Encode()
	if !ok {
		// A copy skips the filter chain entirely: there is nothing to filter in a copied bitstream.
		return nil
	}
	filters := videoFilters(venc)
	if len(filters) == 0 {
		return nil
	}
	return []string{"-vf", strings.Join(filters, ",")}
}

// drawtextFilter renders subtitle text bottom-centered with a translucent box.
func drawtextFilter(textFile string) string {
	return strings.Join([]string{
		"drawtext=textfile=" + escapeFilterArg(textFile),
		"reload=1",
		"fontsize=h/24",
		"fontcolor=white",
		"borderw=2",
		"bordercolor=black",
		"box=1",
		"boxcolor=black@0.45",
		"boxborderw=10",
		"text_align=center",
		"line_spacing=6",
		"x=(w-text_w)/2",
		"y=h-text_h-(h/20)",
	}, ":")
}

// escapeFilterArg escapes a value for ffmpeg's two parsers: the option parser splits on ':', the graph parser on ',', ';', '[' and ']', and both unescape backslashes and quotes.
func escapeFilterArg(s string) string {
	r := strings.NewReplacer(
		`\`, `\\\\`,
		`'`, `\\\'`,
		`:`, `\\:`,
		`,`, `\,`,
		`;`, `\;`,
		`[`, `\[`,
		`]`, `\]`,
	)
	return r.Replace(s)
}
