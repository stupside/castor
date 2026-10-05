package extract

import (
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

var attributeURI = regexp.MustCompile(`URI="([^"]*)"`)

func inspectHLS(body string, base *url.URL) document {
	var d document
	d.ladder = castorv1.Ladder_LADDER_SOLE
	var duration float64
	ended := false
	for line := range strings.Lines(body) {
		line = strings.TrimSpace(line)
		if line == "#EXT-X-ENDLIST" {
			ended = true
		}
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") || strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF:") {
			d.ladder = castorv1.Ladder_LADDER_MULTIVARIANT
		}
		if after, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			value, _, _ := strings.Cut(after, ",")
			n, err := strconv.ParseFloat(value, 64)
			if err == nil && n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) && duration+n < float64(math.MaxInt64)/float64(time.Second) {
				duration += n
			} else {
				duration = math.NaN()
			}
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			d.addReference(line, base)
		}
		for _, m := range attributeURI.FindAllStringSubmatch(line, -1) {
			d.addReference(m[1], base)
		}
	}
	if ended && d.ladder == castorv1.Ladder_LADDER_SOLE && !math.IsNaN(duration) {
		d.runtime = time.Duration(duration * float64(time.Second))
	}
	return d
}
