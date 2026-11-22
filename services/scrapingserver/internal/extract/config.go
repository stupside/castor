package extract

import "time"

// Config is everything an Extractor needs, and the browser and capture sections of the scraping server's configuration.
type Config struct {
	Browser BrowserConfig `yaml:"browser" validate:"required"`
	Capture CaptureConfig `yaml:"capture" validate:"required"`
}

type BrowserConfig struct {
	Timeout    time.Duration `yaml:"timeout" validate:"required,gt=0"`
	Headless   bool          `yaml:"headless"`
	NoSandbox  bool          `yaml:"no_sandbox"`
	ChromePath string        `yaml:"chrome_path"`
}

type CaptureConfig struct {
	// MaxConcurrency bounds how many page URLs are extracted at once, one browser each.
	MaxConcurrency int `yaml:"max_concurrency" validate:"required,min=1"`
}

// Tuning castor ships: how long a page is given, and how far it is driven.
const (
	maxCaptures            = 100
	collectionWindow       = 10 * time.Second
	graceAfterActions      = 15 * time.Second
	preRollWindow          = 60 * time.Second
	navigateIframeTimeout  = 10 * time.Second
	navigateIframeMaxDepth = 5
	turnstileRetryTimeout  = 10 * time.Second
	bypassTurnstileTimeout = 20 * time.Second
	pageBudget             = 2 * time.Minute
)
