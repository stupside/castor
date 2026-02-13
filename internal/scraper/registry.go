package scraper

import (
	"fmt"
	"maps"
	"slices"

	"github.com/stupside/castor/internal/app"
)

// Registry holds all configured scrapers.
type Registry struct {
	scrapers map[string]*Scraper
}

// NewRegistryFromConfig creates a registry from the scraper configs.
func NewRegistryFromConfig(cfgs []app.ScraperConfig) *Registry {
	r := &Registry{
		scrapers: make(map[string]*Scraper, len(cfgs)),
	}
	for _, cfg := range cfgs {
		r.scrapers[cfg.Name] = NewScraper(cfg)
	}
	return r
}

// Get returns a scraper by name.
func (r *Registry) Get(name string) (*Scraper, error) {
	s, ok := r.scrapers[name]
	if !ok {
		return nil, fmt.Errorf("scraper %q not found", name)
	}
	return s, nil
}

// List returns all scraper names.
func (r *Registry) List() []string {
	return slices.Sorted(maps.Keys(r.scrapers))
}
