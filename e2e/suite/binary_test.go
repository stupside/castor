package suite

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/e2e/settings"
)

// binary is castor as a user runs it, one process per cast so no case shares its state with another.
type binary string

func (b binary) Cast(ctx context.Context, launch settings.Launch, args []string) ([]byte, error) {
	var out bytes.Buffer
	cmd := b.command(ctx, launch, args, &out)
	err := cmd.Run()
	if cmd.Process != nil {
		killGroup(cmd)
	}
	return out.Bytes(), err
}

// command runs b in a process group of its own, so the browsers and encoders it starts die with it when ctx ends.
func (b binary) command(ctx context.Context, launch settings.Launch, args []string, out io.Writer) *exec.Cmd {
	// --debug brings the engine's lines back through the watch, so a failing case shows why.
	cmd := exec.CommandContext(ctx, string(b), slices.Concat([]string{"--debug"}, launch.Flags, args)...)
	// Personal endpoints, tokens and media settings must never escape into a fixture's cast.
	env := slices.DeleteFunc(os.Environ(), func(value string) bool {
		return strings.HasPrefix(value, "CASTOR_")
	})
	cmd.Dir, cmd.Env = launch.Dir, append(env, launch.Env...)
	cmd.Stdout, cmd.Stderr = out, out
	// Grandchildren holding the output pipe open must not keep Wait from returning.
	cmd.WaitDelay = 5 * time.Second
	grouped(cmd)
	cmd.Cancel = func() error {
		killGroup(cmd)
		return nil
	}
	return cmd
}

// castor is the binary TestMain builds, the same main package a user runs.
var castor binary

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "castor-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, "castor")
	build := exec.Command("go", "build", "-o", bin, "github.com/stupside/castor/cmd/castor")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building castor: %v\n", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	castor = binary(bin)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
