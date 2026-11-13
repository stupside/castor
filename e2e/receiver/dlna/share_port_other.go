//go:build !linux && !darwin

package dlna

import "syscall"

func sharePort(_, _ string, _ syscall.RawConn) error { return nil }
