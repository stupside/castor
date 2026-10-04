package command

import (
	"fmt"
	"strconv"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/strategy"
)

// sourceTemplates are the routes a catalog source maps a title onto.
var sourceTemplates = map[string]any{"movie": "/embed/movie/{itemID}", "episode": "/embed/tv/{itemID}/{season}-{episode}"}

// Movie builds a title cast through a configured source, as in `movie: tt0111161`.
type Movie struct{}

func (Movie) Name() string { return "movie" }

func (Movie) Build(settings yaml.Node) (Command, error) {
	var id string
	if err := settings.Decode(&id); err != nil || id == "" {
		return nil, fmt.Errorf("movie: want the title's id (%v)", err)
	}
	return title{name: "movie", args: []string{"cast", "movie", id}, route: "/embed/movie/" + id}, nil
}

// Episode builds an episode cast through a configured source, as in `episode: {id: 1399, season: 1, episode: 2}`.
type Episode struct{}

func (Episode) Name() string { return "episode" }

func (Episode) Build(settings yaml.Node) (Command, error) {
	var e struct {
		ID      string `yaml:"id"`
		Season  int    `yaml:"season"`
		Episode int    `yaml:"episode"`
	}
	if err := strategy.Decode(settings, &e); err != nil || e.ID == "" || e.Season < 1 || e.Episode < 1 {
		return nil, fmt.Errorf("episode: want an id, a season and an episode (%v)", err)
	}
	season, episode := strconv.Itoa(e.Season), strconv.Itoa(e.Episode)
	return title{
		name:  "episode",
		args:  []string{"cast", "episode", "--season", season, "--episode", episode, e.ID},
		route: "/embed/tv/" + e.ID + "/" + season + "-" + episode,
	}, nil
}

type title struct {
	name  string
	args  []string
	route string
}

func (c title) Name() string { return c.name }

func (c title) Invoke(t *testing.T, src *origin.Origin) Invocation {
	site := serveSite(t, src, Page{Offer: Fetched})
	return Invocation{
		Args:   c.args,
		Config: map[string]any{"sources": []any{map[string]any{"proxies": []any{site.url}, "templates": sourceTemplates}}},
		Checks: []judge.Check{visited{site: site, route: c.route}},
	}
}
