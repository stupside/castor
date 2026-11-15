//go:build linux || darwin

package dlna

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// sharePort binds beside another socket on the same port, as SO_REUSEPORT lets.
func sharePort(_, _ string, c syscall.RawConn) error {
	var err error
	if control := c.Control(func(fd uintptr) { err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1) }); control != nil {
		return control
	}
	return err
}
