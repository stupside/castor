package awake

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	powerCreateRequest = kernel32.NewProc("PowerCreateRequest")
	powerSetRequest    = kernel32.NewProc("PowerSetRequest")
	powerClearRequest  = kernel32.NewProc("PowerClearRequest")
)

const (
	powerRequestContextSimpleString = 0x1
	powerRequestSystemRequired      = 1
)

// reasonContext is REASON_CONTEXT carrying a simple reason string.
type reasonContext struct {
	version uint32
	flags   uint32
	reason  *uint16
	_       [2]uintptr // the rest of the union
}

// inhibit sets a power request, which shows in powercfg /requests and ends with castor's process even on a crash.
func inhibit() (func(), error) {
	reason, err := windows.UTF16PtrFromString("Casting")
	if err != nil {
		return nil, err
	}
	ctx := reasonContext{flags: powerRequestContextSimpleString, reason: reason}
	h, _, err := powerCreateRequest.Call(uintptr(unsafe.Pointer(&ctx)))
	if windows.Handle(h) == windows.InvalidHandle {
		return nil, err
	}
	request := windows.Handle(h)
	if ok, _, err := powerSetRequest.Call(uintptr(request), powerRequestSystemRequired); ok == 0 {
		windows.CloseHandle(request)
		return nil, err
	}
	return func() {
		powerClearRequest.Call(uintptr(request), powerRequestSystemRequired)
		windows.CloseHandle(request)
	}, nil
}
