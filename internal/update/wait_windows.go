//go:build windows

package update

import (
	"syscall"
	"time"
)

const processSynchronize = 0x00100000

// WaitForExit blocks until process pid has exited or timeout passes. A process
// that can't be opened has already gone.
func WaitForExit(pid int, timeout time.Duration) {
	h, err := syscall.OpenProcess(processSynchronize, false, uint32(pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(h)
	syscall.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
}
