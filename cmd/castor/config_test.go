package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stupside/castor/internal/settings"
	"github.com/urfave/cli/v3"
)

func TestCLIConfigChecksItsOwnSections(t *testing.T) {
	const templates = "sources:\n  - proxies: [https://example.com]\n    templates:\n"
	for _, tc := range []struct {
		name, document, wantError string
	}{
		{"known template", "device:\n  name: Living Room\n  type: dlna\n" + templates + "      movie: /movie/{itemID}\n      episode: /episode/{itemID}\n", ""},
		{"unknown template", "device:\n  name: Living Room\n  type: dlna\n" + templates + "      movvie: /movie/{itemID}\n", "movvie"},
		{"device without type", "device:\n  name: Living Room\n", "device.type'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.document), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := &cli.Command{Name: "castor", Flags: []cli.Flag{settings.Flag}, Action: func(_ context.Context, cmd *cli.Command) error {
				_, err := settings.Load(cmd, Config{})
				return err
			}}
			err := cmd.Run(t.Context(), []string{"castor", "--config", path})
			if tc.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("config error = %v, want one naming %s", err, tc.wantError)
			}
		})
	}
}
