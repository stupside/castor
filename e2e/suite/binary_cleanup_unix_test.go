//go:build unix

package suite

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stupside/castor/e2e/settings"
)

func TestAnExitedCastDoesNotLeaveItsDetachedWorkersRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "castor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30 </dev/null >/dev/null 2>&1 &\necho $!\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := binary(path).Cast(t.Context(), settings.Launch{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatalf("worker PID = %q, %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cast exited successfully but its detached worker is still running")
		}
		time.Sleep(time.Millisecond)
	}
}
