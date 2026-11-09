package encoding

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestHDRToneMappingRunsOnFramesAlreadyBounded(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	for _, tt := range []struct {
		name, input, want string
		width             int
		height            media.HeightCap
	}{
		{"wide 4K", "3840x1608", "1920x804", 1920, 1080},
		{"portrait", "1080x1920", "608x1080", 1920, 1080},
		{"small source is not upscaled", "960x540", "960x540", 1920, 1080},
		{"height only", "3840x1608", "1920x804", 0, 804},
	} {
		t.Run(tt.name, func(t *testing.T) {
			graph := strings.Join(videoFilters(codec.VideoEncode{ToneMap: true, MaxWidth: tt.width, MaxHeight: tt.height, MaxFrameRate: 30}), ",")
			// Observe the pixels entering Hable, not the geometry named in an argv.
			graph = strings.Replace(graph, "tonemap=", "showinfo@mapped,tonemap=", 1) + ",showinfo@output"
			out, err := exec.CommandContext(t.Context(), binary, "-hide_banner",
				"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%s:rate=1:duration=1", tt.input),
				"-vf", "format=yuv420p10le,setparams=range=limited:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc,"+graph,
				"-frames:v", "1", "-f", "null", "-").CombinedOutput()
			if err != nil {
				t.Fatalf("running the HDR chain: %v\n%s", err, out)
			}
			var mapped, output bool
			for line := range strings.SplitSeq(string(out), "\n") {
				mapped = mapped || strings.Contains(line, "showinfo@mapped") && strings.Contains(line, "fmt:gbrpf32le") && strings.Contains(line, "s:"+tt.want+" ")
				output = output || strings.Contains(line, "showinfo@output") && strings.Contains(line, "fmt:yuv420p ") && strings.Contains(line, "s:"+tt.want+" ")
			}
			if !mapped || !output || !strings.Contains(string(out), "color_primaries:bt709 color_trc:bt709") {
				t.Fatalf("want %s linear frames into the tone map and %s BT.709 SDR out\n%s", tt.want, tt.want, out)
			}
		})
	}
}
