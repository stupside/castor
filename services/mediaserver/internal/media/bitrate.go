package media

import (
	"fmt"
	"strconv"
)

// Bitrate is bits per second; only a declared rate is a ceiling for choosing, never a measured one.
type Bitrate int64

// Admits reports whether rate fits under the ceiling b; an unmeasured rate never does.
func (b Bitrate) Admits(rate Bitrate) bool { return b == 0 || (rate > 0 && rate <= b) }

// String is the rate in the unit a person reads; none is no bound.
func (b Bitrate) String() string {
	if b == 0 {
		return "unbounded"
	}
	return fmt.Sprintf("%.1f Mbit/s", float64(b)/1e6)
}

// FFmpeg is the rate as ffmpeg parses it.
func (b Bitrate) FFmpeg() string { return strconv.FormatInt(int64(b)/1000, 10) + "k" }
