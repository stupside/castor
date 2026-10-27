package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

// sections stands for a command's own view of castor's configuration.
type sections struct {
	Cast struct {
		MaxHeight uint32 `yaml:"max_height"`
	} `yaml:"cast"`
	Servers struct {
		API struct {
			Network struct {
				Timeout time.Duration `yaml:"timeout"`
			} `yaml:"network"`
		} `yaml:"api"`
	} `yaml:"servers"`
}

func defaulted() sections {
	var s sections
	s.Servers.API.Network.Timeout = 5 * time.Second
	return s
}

// write lays config.yaml, and its overlay when given, in a fresh directory, and returns the file's path.
func write(t *testing.T, yaml, local string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if yaml != "" {
		if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.local.yaml"), []byte(local), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestTheOverlayThenTheEnvironmentWinOverTheFileAndTheDefaults(t *testing.T) {
	path := write(t, "cast:\n  max_height: 1080\nservers:\n  api:\n    network:\n      timeout: 2s\n", "cast:\n  max_height: 720\n")
	got, err := read([]string{path}, defaulted())
	if err != nil {
		t.Fatal(err)
	}
	if got.Cast.MaxHeight != 720 || got.Servers.API.Network.Timeout != 2*time.Second {
		t.Errorf("read %+v, want the overlay's height over the file's, and the file's timeout over the default", got)
	}

	t.Setenv("CASTOR_CAST__MAX_HEIGHT", "480")
	if got, err = read([]string{path}, defaulted()); err != nil {
		t.Fatal(err)
	}
	if got.Cast.MaxHeight != 480 {
		t.Errorf("max_height = %d, want the environment's 480", got.Cast.MaxHeight)
	}
}

func TestNoFileLeavesTheDefaults(t *testing.T) {
	got, err := read([]string{filepath.Join(t.TempDir(), "config.yaml")}, defaulted())
	if err != nil || got.Servers.API.Network.Timeout != 5*time.Second {
		t.Errorf("read %+v %v without a file, want the defaults", got, err)
	}
}

func TestTheNamedFileThenTheWorkingDirectoryWinOverTheUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(userDir, "castor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "castor", "config.yaml"), []byte("cast:\n  max_height: 1080\nservers:\n  api:\n    network:\n      timeout: 2s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(write(t, "cast:\n  max_height: 720\n", "")))
	named := write(t, "cast:\n  max_height: 480\n", "")

	load := func(args ...string) sections {
		t.Helper()
		var got *sections
		cmd := &cli.Command{Name: "castor", Flags: []cli.Flag{Flag}, Action: func(_ context.Context, cmd *cli.Command) (err error) {
			got, err = Load(cmd, defaulted())
			return err
		}}
		if err := cmd.Run(t.Context(), append([]string{"castor"}, args...)); err != nil {
			t.Fatal(err)
		}
		return *got
	}
	if got := load(); got.Cast.MaxHeight != 720 || got.Servers.API.Network.Timeout != 2*time.Second {
		t.Errorf("loaded %+v, want the working directory's height over the user's, and the user's timeout", got)
	}
	if got := load("--config", named); got.Cast.MaxHeight != 480 || got.Servers.API.Network.Timeout != 2*time.Second {
		t.Errorf("loaded %+v with --config, want the named file's height over both, and the user's timeout", got)
	}
}

func TestAValidationFailureNamesTheConfigurationKeyAndAnEmptyRequiredSectionFails(t *testing.T) {
	type endpoint struct {
		Token string `yaml:"token" validate:"required"`
	}
	var cfg struct {
		Cast struct {
			MaxHeight uint32 `yaml:"max_height"`
		} `yaml:"cast" validate:"required"`
		Servers struct {
			Media struct {
				endpoint `yaml:",inline"`
			} `yaml:"media"`
		} `yaml:"servers"`
	}
	_, err := read([]string{write(t, "", "")}, cfg)
	if err == nil || !strings.Contains(err.Error(), "'cast'") || !strings.Contains(err.Error(), "'servers.media.token'") {
		t.Errorf("read = %v, want the empty cast section and servers.media.token named", err)
	}
}

func TestAnUnreadableConfigPathIsNotTreatedAsAMissingFile(t *testing.T) {
	path := write(t, "network:\n  timeout: 2s\n", "")
	if _, err := read([]string{filepath.Join(path, "config.yaml")}, defaulted()); err == nil {
		t.Error("a path through a regular file was ignored instead of reporting the filesystem error")
	}
}

type ownedSections struct {
	Cast struct {
		MaxHeight uint32 `yaml:"max_height"`
	} `yaml:"cast" settings:"strict"`
}

func TestUnknownKeysFailInACommandOwnedSection(t *testing.T) {
	path := write(t, "cast:\n  max_heigth: 1080\nservers:\n  media:\n    token: secret\n", "")
	if _, err := read([]string{path}, ownedSections{}); err == nil || !strings.Contains(err.Error(), "max_heigth") {
		t.Errorf("config returned %v, want the unknown cast key", err)
	}
	t.Setenv("CASTOR_CAST__MAX_HEIGTH", "720")
	if _, err := read([]string{filepath.Join(t.TempDir(), "config.yaml")}, ownedSections{}); err == nil || !strings.Contains(err.Error(), "max_heigth") {
		t.Errorf("environment returned %v, want the unknown cast key", err)
	}
}

func TestOtherCommandsSectionsAreIgnoredByAnOwnedSectionCheck(t *testing.T) {
	path := write(t, "cast:\n  max_height: 1080\nservers:\n  media:\n    token: secret\n", "")
	if _, err := read([]string{path}, ownedSections{}); err != nil {
		t.Fatalf("unrelated media key was rejected: %v", err)
	}
}

func TestKnownKeysInOtherCommandsSectionsRemainValid(t *testing.T) {
	path := write(t, "servers:\n  api:\n    token: secret\n  scraping:\n    browser:\n      timeout: 30s\nsources:\n  - proxies: [https://example.com]\n    templates:\n      movie: /watch\n      episode: /episode\n", "")
	if _, err := read([]string{path}, defaulted()); err != nil {
		t.Fatalf("known keys used by other commands were rejected: %v", err)
	}
}

func TestExampleConfigurationUsesKnownKeys(t *testing.T) {
	example, err := os.ReadFile("../../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := write(t, string(example), "")
	if _, err := read([]string{path}, defaulted()); err != nil {
		t.Fatalf("example configuration rejected: %v", err)
	}
}
