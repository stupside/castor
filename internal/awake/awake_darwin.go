package awake

import (
	"os"
	"os/exec"
	"strconv"
)

// inhibit runs caffeinate, whose -w ends it, and its assertion, with castor even if castor crashes.
func inhibit() (func(), error) {
	cmd := exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() {
		cmd.Process.Kill()
		cmd.Wait()
	}, nil
}
