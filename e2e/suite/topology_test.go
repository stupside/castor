package suite

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/stupside/castor/e2e/settings"
	"github.com/stupside/castor/e2e/strategy"
)

// topology is how castor's processes are laid out for one cast; the command's output and exit are what it returns.
type topology interface {
	strategy.Named
	Cast(t *testing.T, ctx context.Context, carrier settings.Carrier, doc map[string]any, args []string) ([]byte, error)
}

// embedded is castor as most users run it: one command, all three services inside it.
type embedded struct{}

func (embedded) Name() string { return "embedded" }

func (embedded) Cast(t *testing.T, ctx context.Context, carrier settings.Carrier, doc map[string]any, args []string) ([]byte, error) {
	launch, err := carrier.Carry(t, doc)
	if err != nil {
		t.Fatal(err)
	}
	return castor.Cast(ctx, launch, args)
}

// split runs all three services in separate processes, with independent tokens, and castor as their client.
type split struct{}

func (split) Name() string { return "split" }

func (split) Cast(t *testing.T, ctx context.Context, carrier settings.Carrier, doc map[string]any, args []string) ([]byte, error) {
	mediaToken, apiToken, scrapingToken := rand.Text(), rand.Text(), rand.Text()
	// Port 0 so parallel cases never race for one; castor's config only accepts it on every interface.
	media := start(t, carrier, with(t, doc, map[string]any{"server": map[string]any{"listen": ":0", "token": mediaToken}}), "media-server")
	mediaURL := media.ready(t, mediaToken)
	scraping := start(t, carrier, with(t, doc, map[string]any{"scraping": map[string]any{"listen": ":0", "token": scrapingToken}}), "scraping-server")
	scrapingURL := scraping.ready(t, scrapingToken)
	api := start(t, carrier, with(t, doc, map[string]any{
		"server":   map[string]any{"url": mediaURL, "token": mediaToken},
		"scraping": map[string]any{"url": scrapingURL, "token": scrapingToken},
		"api":      map[string]any{"listen": ":0", "token": apiToken},
	}), "api-server")
	apiURL := api.ready(t, apiToken)

	launch, err := carrier.Carry(t, with(t, doc, map[string]any{"api": map[string]any{"url": apiURL, "token": apiToken}}))
	if err != nil {
		t.Fatal(err)
	}
	return castor.Cast(ctx, launch, args)
}
