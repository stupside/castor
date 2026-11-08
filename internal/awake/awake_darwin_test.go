package awake

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// caffeinating counts this process's caffeinate helpers.
func caffeinating() int {
	out, _ := exec.Command("pgrep", "-f", "caffeinate -i -w "+strconv.Itoa(os.Getpid())).Output()
	return strings.Count(string(out), "\n")
}

func TestHoldsShareOneAssertionUntilTheLastRelease(t *testing.T) {
	first, second := Hold(), Hold()
	if n := caffeinating(); n != 1 {
		t.Fatalf("two holds ran %d caffeinate, want 1", n)
	}
	first()
	first()
	if n := caffeinating(); n != 1 {
		t.Fatalf("releasing one hold, twice, left %d caffeinate, want 1", n)
	}
	second()
	if n := caffeinating(); n != 0 {
		t.Fatalf("releasing every hold left %d caffeinate, want 0", n)
	}
}
