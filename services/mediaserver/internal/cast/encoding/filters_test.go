package encoding

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestHDRBoundsPrecedeFloatingPointToneMapping(t *testing.T) {
	filters := videoFilters(codec.VideoEncode{ToneMap: true, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRate: 30})
	if len(filters) != 2 || !strings.HasPrefix(filters[0], "fps=") || !strings.HasPrefix(filters[1], "zscale=w=") {
		t.Fatalf("HDR frames must be rate- and size-bounded before tone mapping: %v", filters)
	}
	if !strings.Contains(filters[1], "t=linear:npl=100") || !strings.Contains(filters[1], "tonemap=tonemap=hable") || !strings.HasSuffix(filters[1], "format=yuv420p") {
		t.Fatalf("HDR conversion must retain linear-light Hable mapping to SDR: %v", filters)
	}
}

func TestHDRToneMappingBoundsTheFramesItActuallyProcesses(t *testing.T) {
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
			graph := strings.Join(videoFilters(codec.VideoEncode{ToneMap: true, MaxWidth: tt.width, MaxHeight: tt.height}), ",")
			// Observe the pixels entering Hable, not just the geometry named in an argv.
			graph = strings.Replace(graph, "tonemap=", "showinfo@bounded,tonemap=", 1) + ",showinfo@output"
			args := []string{"-hide_banner", "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%s:rate=1:duration=1", tt.input),
				"-vf", "format=yuv420p10le,setparams=range=limited:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc," + graph,
				"-frames:v", "1", "-f", "null", "-"}
			out, err := exec.CommandContext(t.Context(), binary, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("executing HDR filters: %v\n%s", err, out)
			}
			var bounded, output bool
			for line := range strings.SplitSeq(string(out), "\n") {
				if strings.Contains(line, "showinfo@bounded") && strings.Contains(line, "fmt:gbrpf32le") && strings.Contains(line, "s:"+tt.want+" ") {
					bounded = true
				}
				if strings.Contains(line, "showinfo@output") && strings.Contains(line, "fmt:yuv420p ") && strings.Contains(line, "s:"+tt.want+" ") {
					output = true
				}
			}
			if !bounded || !output || !strings.Contains(string(out), "color_primaries:bt709 color_trc:bt709") {
				t.Fatalf("expected bounded linear-light frames and %s BT.709 SDR output\n%s", tt.want, out)
			}
		})
	}
}

func TestAPictureFromATimeIsSelectedBeforeEveryOtherFilter(t *testing.T) {
	got := videoFilters(codec.VideoEncode{From: 1500 * time.Millisecond, Deinterlace: true, MaxFrameRate: 30})
	if len(got) < 3 || got[0] != "select='gte(t,1.500000)'" {
		t.Fatalf("filters %v, want the selection first", got)
	}
	if got := videoFilters(codec.VideoEncode{MaxFrameRate: 30}); len(got) != 1 || strings.HasPrefix(got[0], "select") {
		t.Fatalf("filters %v, want no selection where the picture begins with the read", got)
	}
}
