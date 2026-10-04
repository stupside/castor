package scrapingserver

import (
	"time"

	"github.com/stupside/castor/services/scrapingserver/internal/extract"
)

// Config contains only browser and capture concerns; playback settings do not belong here.
type Config struct {
	Browser extract.BrowserConfig `yaml:"browser" validate:"required"`
	Capture extract.CaptureConfig `yaml:"capture" validate:"required"`
}

func Defaults() Config {
	return Config{Browser: extract.BrowserConfig{Timeout: 30 * time.Second, Headless: true}, Capture: extract.CaptureConfig{MaxConcurrency: 4}}
}

// newExtractor builds the page resolver with the formats understood by Castor.
func newExtractor(c Config) *extract.Extractor {
	return extract.New(extract.Config{Browser: c.Browser, Capture: c.Capture})
}
