package playlist

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
	"github.com/stupside/castor/e2e/strategy"
)

// omission is a variant attribute a master leaves out; upper-cased, it is the attribute's name.
type omission string

const (
	omitsResolution omission = "resolution"
	omitsCodecs     omission = "codecs"
)

type codecForm string

// legacyCodecs writes avc1 in decimal profile.level, as Wowza and older Apple tools do.
const legacyCodecs codecForm = "legacy"

type bandwidths string

// flatBandwidth declares every variant at the largest BANDWIDTH, so no variant stands out by bitrate.
const flatBandwidth bandwidths = "flat"

// RewritesMaster builds a behaviour serving the master as a sloppier packager writes it, as in `rewrites-master: {omit: [resolution]}`.
type RewritesMaster struct{}

func (RewritesMaster) Name() string { return "rewrites-master" }

func (RewritesMaster) Build(settings yaml.Node) (origin.Behaviour, error) {
	var m rewritesMaster
	if err := strategy.Decode(settings, &m); err != nil {
		return nil, fmt.Errorf("rewrites-master: %w", err)
	}
	switch {
	case len(m.Omit) == 0 && m.Codecs == "" && m.Bandwidth == "":
		return nil, fmt.Errorf("rewrites-master: want at least one of omit, codecs, bandwidth")
	case slices.ContainsFunc(m.Omit, func(o omission) bool { return o != omitsResolution && o != omitsCodecs }):
		return nil, fmt.Errorf("rewrites-master: want omit among %s, %s", omitsResolution, omitsCodecs)
	case m.Codecs != "" && m.Codecs != legacyCodecs:
		return nil, fmt.Errorf("rewrites-master: want codecs %s", legacyCodecs)
	case m.Bandwidth != "" && m.Bandwidth != flatBandwidth:
		return nil, fmt.Errorf("rewrites-master: want bandwidth %s", flatBandwidth)
	case m.Codecs != "" && slices.Contains(m.Omit, omitsCodecs):
		return nil, fmt.Errorf("rewrites-master: cannot rewrite codecs it omits")
	}
	return m, nil
}

type rewritesMaster struct {
	Omit      []omission `yaml:"omit"`
	Codecs    codecForm  `yaml:"codecs"`
	Bandwidth bandwidths `yaml:"bandwidth"`
}

func (rewritesMaster) Name() string { return "rewrites-master" }

func (m rewritesMaster) Wrap(next http.Handler, _ origin.Published) http.Handler {
	return serving.RewritesPlaylists(next, func(playlist string) (string, bool) {
		if !strings.Contains(playlist, streamInf) {
			return playlist, false
		}
		return m.rewrite(playlist), true
	})
}

func (m rewritesMaster) rewrite(master string) string {
	largest := 0
	for line := range strings.Lines(master) {
		if list, ok := strings.CutPrefix(strings.TrimSpace(line), streamInf); ok {
			for _, a := range attributes(list) {
				if n, err := strconv.Atoi(a.value); a.key == "BANDWIDTH" && err == nil {
					largest = max(largest, n)
				}
			}
		}
	}
	var out strings.Builder
	for line := range strings.Lines(master) {
		list, ok := strings.CutPrefix(strings.TrimSpace(line), streamInf)
		if !ok {
			out.WriteString(line)
			continue
		}
		as := slices.DeleteFunc(attributes(list), func(a attribute) bool {
			return slices.Contains(m.Omit, omission(strings.ToLower(a.key))) ||
				(m.Bandwidth == flatBandwidth && a.key == "AVERAGE-BANDWIDTH")
		})
		for i, a := range as {
			switch {
			case a.key == "BANDWIDTH" && m.Bandwidth == flatBandwidth:
				as[i].value = strconv.Itoa(largest)
			case a.key == "CODECS" && m.Codecs == legacyCodecs:
				as[i].value = legacy(a.value)
			}
		}
		out.WriteString(streamInf)
		out.WriteString(joined(as))
		out.WriteString("\n")
	}
	return out.String()
}

// legacy rewrites each avc1.PPCCLL in a quoted CODECS list to avc1.<profile>.<level> in decimal, leaving other entries alone.
func legacy(codecs string) string {
	entries := strings.Split(strings.Trim(codecs, `"`), ",")
	for i, e := range entries {
		hex, ok := strings.CutPrefix(e, "avc1.")
		if !ok || len(hex) != 6 {
			continue
		}
		profile, perr := strconv.ParseUint(hex[:2], 16, 8)
		level, lerr := strconv.ParseUint(hex[4:], 16, 8)
		if perr == nil && lerr == nil {
			entries[i] = fmt.Sprintf("avc1.%d.%d", profile, level)
		}
	}
	return `"` + strings.Join(entries, ",") + `"`
}
