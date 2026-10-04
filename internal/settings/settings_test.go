package settings

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sections stands for a command's own view of castor's configuration.
type sections struct {
	Cast struct {
		MaxHeight uint32 `yaml:"max_height"`
	} `yaml:"cast"`
	Network struct {
		Timeout time.Duration `yaml:"timeout"`
	} `yaml:"network"`
}

func defaulted() sections {
	var s sections
	s.Network.Timeout = 5 * time.Second
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
	path := write(t, "cast:\n  max_height: 1080\nnetwork:\n  timeout: 2s\n", "cast:\n  max_height: 720\n")
	got, err := Read(path, defaulted())
	if err != nil {
		t.Fatal(err)
	}
	if got.Cast.MaxHeight != 720 || got.Network.Timeout != 2*time.Second {
		t.Errorf("read %+v, want the overlay's height over the file's, and the file's timeout over the default", got)
	}

	t.Setenv("CASTOR_CAST__MAX_HEIGHT", "480")
	if got, err = Read(path, defaulted()); err != nil {
		t.Fatal(err)
	}
	if got.Cast.MaxHeight != 480 {
		t.Errorf("max_height = %d, want the environment's 480", got.Cast.MaxHeight)
	}
}

func TestNoFileLeavesTheDefaults(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "config.yaml"), defaulted())
	if err != nil || got.Network.Timeout != 5*time.Second {
		t.Errorf("read %+v %v without a file, want the defaults", got, err)
	}
}

func TestAnUnreadableConfigPathIsNotTreatedAsAMissingFile(t *testing.T) {
	path := write(t, "network:\n  timeout: 2s\n", "")
	if _, err := Read(filepath.Join(path, "config.yaml"), defaulted()); err == nil {
		t.Error("a path through a regular file was ignored instead of reporting the filesystem error")
	}
}
