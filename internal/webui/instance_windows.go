//go:build windows

package webui

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	user32           = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW  = user32.NewProc("MessageBoxW")
)

const mbIconInformation = 0x40

// acquireInstance takes the lock for cfgPath and reports whether this is the
// only copy using that config. The handle stays open for the life of the
// process, and Windows releases it on exit, so a crash leaves no stale lock.
func acquireInstance(cfgPath string) (bool, error) {
	name, err := syscall.UTF16PtrFromString(instanceMutexName(cfgPath))
	if err != nil {
		return false, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return false, fmt.Errorf("CreateMutexW: %w", callErr)
	}
	if errors.Is(callErr, syscall.ERROR_ALREADY_EXISTS) {
		syscall.CloseHandle(syscall.Handle(h))
		return false, nil
	}
	return true, nil
}

// showAlreadyRunning tells the user why nothing opened; a windowsgui build has
// no console to log to.
func showAlreadyRunning() {
	text, err := syscall.UTF16PtrFromString(alreadyRunningMsg)
	if err != nil {
		return
	}
	title, err := syscall.UTF16PtrFromString("BarnCat Sparks")
	if err != nil {
		return
	}
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconInformation)
}
