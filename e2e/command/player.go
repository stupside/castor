package command

import (
	"cmp"
	"fmt"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/strategy"
)

// Player builds a cast of a web page whose script requests the stream, so castor's browser must find it among the
// page's decoys, as in `player: {decoys: [ad-clip, blob], stream: {delayed: 30s}}`; the stream is fetched unless named.
type Player struct {
	Offers strategy.Registry[strategy.Factory[Offer]]
	Decoys strategy.Registry[Decoy]
}

func (Player) Name() string { return "player" }

func (p Player) Build(raw yaml.Node) (Command, error) {
	var settings struct {
		Decoys []string        `yaml:"decoys"`
		Stream strategy.Choice `yaml:"stream"`
	}
	if err := strategy.Decode(raw, &settings); err != nil {
		return nil, fmt.Errorf("player: %w", err)
	}
	settings.Stream.Name = cmp.Or(settings.Stream.Name, Fetched.Name())
	offer, err := strategy.Build(p.Offers, settings.Stream)
	if err != nil {
		return nil, fmt.Errorf("player.stream: %w", err)
	}
	page := Page{Offer: offer}
	for _, name := range settings.Decoys {
		decoy, err := p.Decoys.Lookup(name)
		if err != nil {
			return nil, fmt.Errorf("player.decoys: %w", err)
		}
		page.Decoys = append(page.Decoys, decoy)
	}
	return player{page: page}, nil
}

type player struct{ page Page }

func (player) Name() string { return "player" }

func (p player) Invoke(t *testing.T, src *origin.Origin) Invocation {
	site := serveSite(t, src, p.page)
	return Invocation{Args: []string{"cast", "player", site.url + "/watch"}, Checks: []judge.Check{visited{site: site, route: "/watch"}}}
}
