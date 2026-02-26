//go:build windows

package midi

import (
	"syscall"
	"unsafe"
)

func EnumerateMIDIDevices() []string {
	count, _, _ := midiInGetNumDevs.Call()
	if count == 0 {
		return nil
	}
	names := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		var caps midiInCapsW
		ret, _, _ := midiInGetDevCapsW.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
		if ret != 0 {
			continue
		}
		names = append(names, syscall.UTF16ToString(caps.szPname[:]))
	}
	return names
}
