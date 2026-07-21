package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileWithEnvVars(t *testing.T) {
	t.Setenv("CASTOR_DEVICE__NAME", "Xiaomi TV Box")
	t.Setenv("CASTOR_DEVICE__TYPE", "chromecast")

	cfg, err := Load("/tmp/nonexistent-config-293478.yaml")
	if err != nil {
		t.Fatalf("Load should succeed when config file is missing and env vars supply required fields: %v", err)
	}
	if cfg.Device.Name != "Xiaomi TV Box" {
		t.Errorf("device.name should come from env var, got %q", cfg.Device.Name)
	}
	if string(cfg.Device.Type) != "chromecast" {
		t.Errorf("device.type should come from env var, got %q", cfg.Device.Type)
	}
	if cfg.Network.Timeout != 5*time.Second {
		t.Errorf("network.timeout should default to 5s, got %s", cfg.Network.Timeout)
	}
	if cfg.Resolver.MaxHeight != 1080 {
		t.Errorf("resolver.max_height should default to 1080, got %d", cfg.Resolver.MaxHeight)
	}
}

func TestLoadLocalOverlay(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(base, []byte("device:\n  name: tv\n  type: dlna\ntmdb:\n  api_key: placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.local.yaml"), []byte("tmdb:\n  api_key: real\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TMDB.APIKey != "real" {
		t.Errorf("config.local.yaml should overlay the base config: api_key = %q", cfg.TMDB.APIKey)
	}
	if cfg.Device.Name != "tv" {
		t.Errorf("base values outside the overlay must survive: device.name = %q", cfg.Device.Name)
	}
	// A required-with-default field is present without the user setting it.
	if cfg.Resolver.MaxHeight != 1080 {
		t.Errorf("resolver.max_height should default to 1080, got %d", cfg.Resolver.MaxHeight)
	}
}

// TestLoadEmptySectionKeepsDefaults guards the footgun where a section header
// with every field commented out parses to null. That null must not wipe the
// defaults beneath it, or validation fails on fields the user never touched.
func TestLoadEmptySectionKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	// `resolver:` with only a comment underneath is YAML null, exactly like
	// the shipped template's optional-override block.
	if err := os.WriteFile(base, []byte("device:\n  name: tv\n  type: dlna\nresolver:\n  # max_height: 1080\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(base)
	if err != nil {
		t.Fatalf("null section wiped defaults and broke validation: %v", err)
	}
	if cfg.Resolver.MaxHeight != 1080 {
		t.Errorf("resolver.max_height should still default to 1080, got %d", cfg.Resolver.MaxHeight)
	}
	if cfg.Resolver.FFprobePath != "ffprobe" {
		t.Errorf("resolver.ffprobe_path should still default, got %q", cfg.Resolver.FFprobePath)
	}
	// Durations come from the typed defaults as real time.Duration values, not
	// "30s" strings; make sure they survive the decode intact.
	if cfg.Resolver.HLSTimeout != 30*time.Second {
		t.Errorf("resolver.hls_timeout should default to 30s, got %s", cfg.Resolver.HLSTimeout)
	}
	if cfg.Network.Timeout != 5*time.Second {
		t.Errorf("network.timeout should default to 5s, got %s", cfg.Network.Timeout)
	}
}

// TestLoadFileOverridesDefault confirms the layering direction: a value the
// file sets must win over the typed default, including durations parsed from
// the "30s" string form.
func TestLoadFileOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(base, []byte("device:\n  name: tv\n  type: dlna\nresolver:\n  max_height: 2160\nnetwork:\n  timeout: 12s\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resolver.MaxHeight != 2160 {
		t.Errorf("file should override the default: max_height = %d", cfg.Resolver.MaxHeight)
	}
	if cfg.Network.Timeout != 12*time.Second {
		t.Errorf("file should override the default duration: network.timeout = %s", cfg.Network.Timeout)
	}
	// A sibling default the file didn't touch must remain.
	if cfg.Resolver.ProbeMaxConcurrency != 2 {
		t.Errorf("untouched sibling default should remain: probe_max_concurrency = %d", cfg.Resolver.ProbeMaxConcurrency)
	}
}
