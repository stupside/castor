package playlist

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
	"github.com/stupside/castor/e2e/strategy"
)

// AlternateAudio builds a behaviour declaring the muxed audio as a URI-less English default beside a URI'd alternate, as in `alternate-audio: {language: es}`.
type AlternateAudio struct{}

func (AlternateAudio) Name() string { return "alternate-audio" }

func (AlternateAudio) Build(settings yaml.Node) (origin.Behaviour, error) {
	var a alternateAudio
	if err := strategy.Decode(settings, &a); err != nil {
		return nil, fmt.Errorf("alternate-audio: %w", err)
	}
	if a.Language == "" || a.Language == "en" || strings.ContainsAny(a.Language, "\",\r\n") {
		return nil, fmt.Errorf("alternate-audio: want the alternate's language, a plain tag other than the default en")
	}
	return a, nil
}

type alternateAudio struct {
	Language string `yaml:"language"`
}

func (alternateAudio) Name() string { return "alternate-audio" }

func (a alternateAudio) Wrap(next http.Handler, _ origin.Published) http.Handler {
	return serving.RewritesPlaylists(next, func(playlist string) (string, bool) {
		if !strings.Contains(playlist, streamInf) || strings.Contains(playlist, "#EXT-X-MEDIA:TYPE=AUDIO") {
			return playlist, false
		}
		return a.rewrite(playlist), true
	})
}

func (a alternateAudio) rewrite(master string) string {
	lines := slices.Collect(strings.Lines(master))
	var out strings.Builder
	declared := false
	for i, line := range lines {
		if !strings.HasPrefix(line, streamInf) {
			out.WriteString(line)
			continue
		}
		if !declared {
			out.WriteString(`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",LANGUAGE="en",NAME="English",DEFAULT=YES,AUTOSELECT=YES` + "\n")
			fmt.Fprintf(&out, `#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",LANGUAGE="%s",NAME="%s",DEFAULT=NO,AUTOSELECT=YES,URI="%s"`+"\n", a.Language, a.Language, variant(lines[i+1:]))
			declared = true
		}
		out.WriteString(strings.TrimRight(line, "\r\n"))
		out.WriteString(`,AUDIO="aud"`)
		out.WriteString("\n")
	}
	return out.String()
}

// variant is the URI line a STREAM-INF tag applies to: the first line after it that is not blank or a tag.
func variant(after []string) string {
	for _, line := range after {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}
