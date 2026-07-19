package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
