//go:build windows

package hidknob

import (
	"log"
	"strings"
	"syscall"
	"unsafe"
)

// supported reports whether this platform can read the knob.
const supported = true

const (
	knobVIDPID = "vid_0483&pid_5742" // matched case-insensitively in the device path

	digcfPresent         = 0x2
	digcfDeviceInterface = 0x10

	genericRead        = 0x80000000
	genericWrite       = 0x40000000
	fileShareRead      = 0x1
	fileShareWrite     = 0x2
	openExisting       = 3
	fileFlagOverlapped = 0x40000000 // WinUSB requires an overlapped handle

	pipeTransferTimeout = 0x03 // WinUsb_SetPipePolicy PIPE_TRANSFER_TIMEOUT
	usbdPipeInterrupt   = 3    // USBD_PIPE_TYPE UsbdPipeTypeInterrupt
	readTimeoutMillis   = 250

	errorSemTimeout syscall.Errno = 121 // ERROR_SEM_TIMEOUT: ReadPipe timed out
)

// guidDevInterfaceUSBDevice is GUID_DEVINTERFACE_USB_DEVICE
// {A5DCBF10-6530-11D2-901F-00C04FB951ED}.
var guidDevInterfaceUSBDevice = syscall.GUID{
	Data1: 0xA5DCBF10, Data2: 0x6530, Data3: 0x11D2,
	Data4: [8]byte{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED},
}

var (
	modSetupapi = syscall.NewLazyDLL("setupapi.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modWinusb   = syscall.NewLazyDLL("winusb.dll")

	procSetupDiGetClassDevsW             = modSetupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumDeviceInterfaces      = modSetupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetDeviceInterfaceDetailW = modSetupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procSetupDiDestroyDeviceInfoList     = modSetupapi.NewProc("SetupDiDestroyDeviceInfoList")

	procCreateFileW = modKernel32.NewProc("CreateFileW")

	procWinUsbInitialize             = modWinusb.NewProc("WinUsb_Initialize")
	procWinUsbFree                   = modWinusb.NewProc("WinUsb_Free")
	procWinUsbQueryInterfaceSettings = modWinusb.NewProc("WinUsb_QueryInterfaceSettings")
	procWinUsbQueryPipe              = modWinusb.NewProc("WinUsb_QueryPipe")
	procWinUsbSetPipePolicy          = modWinusb.NewProc("WinUsb_SetPipePolicy")
	procWinUsbReadPipe               = modWinusb.NewProc("WinUsb_ReadPipe")
	procWinUsbAbortPipe              = modWinusb.NewProc("WinUsb_AbortPipe")
)

// spDeviceInterfaceData matches SP_DEVICE_INTERFACE_DATA.
type spDeviceInterfaceData struct {
	CbSize             uint32
	InterfaceClassGuid syscall.GUID
	Flags              uint32
	Reserved           uintptr
}

// usbInterfaceDescriptor matches USB_INTERFACE_DESCRIPTOR.
type usbInterfaceDescriptor struct {
	BLength            uint8
	BDescriptorType    uint8
	BInterfaceNumber   uint8
	BAlternateSetting  uint8
	BNumEndpoints      uint8
	BInterfaceClass    uint8
	BInterfaceSubClass uint8
	BInterfaceProtocol uint8
	IInterface         uint8
}

// winusbPipeInformation matches WINUSB_PIPE_INFORMATION.
type winusbPipeInformation struct {
	PipeType          int32
	PipeId            uint8
	MaximumPacketSize uint16
	Interval          uint8
}

// winusbTransport is an open knob on the WinUSB driver.
type winusbTransport struct {
	handle syscall.Handle
	wu     uintptr
	pipe   uint8
}

func openDevice() (transport, State, error) {
	path := findKnobPath()
	if path == "" {
		return nil, StateNotConnected, ErrNotConnected
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, StateNotConnected, ErrNotConnected
	}
	r, _, _ := procCreateFileW.Call(uintptr(unsafe.Pointer(p)), genericRead|genericWrite,
		fileShareRead|fileShareWrite, 0, openExisting, fileFlagOverlapped, 0)
	h := syscall.Handle(r)
	if h == syscall.InvalidHandle {
		// Plugged in, but not openable for WinUSB: still on the HID driver.
		return nil, StateNotSetUp, ErrNotSetUp
	}
	var wu uintptr
	if ok, _, _ := procWinUsbInitialize.Call(uintptr(h), uintptr(unsafe.Pointer(&wu))); ok == 0 {
		syscall.CloseHandle(h)
		return nil, StateNotSetUp, ErrNotSetUp
	}
	pipe, found := interruptInPipe(wu)
	if !found {
		log.Printf("knob: no interrupt IN endpoint on interface 0")
		procWinUsbFree.Call(wu)
		syscall.CloseHandle(h)
		return nil, StateNotSetUp, ErrNotSetUp
	}
	timeout := uint32(readTimeoutMillis)
	procWinUsbSetPipePolicy.Call(wu, uintptr(pipe), pipeTransferTimeout, unsafe.Sizeof(timeout), uintptr(unsafe.Pointer(&timeout)))
	return &winusbTransport{handle: h, wu: wu, pipe: pipe}, StateConnected, nil
}

// findKnobPath returns the device interface path of the knob, or "" if it
// isn't plugged in.
func findKnobPath() string {
	devs, _, _ := procSetupDiGetClassDevsW.Call(uintptr(unsafe.Pointer(&guidDevInterfaceUSBDevice)), 0, 0, digcfPresent|digcfDeviceInterface)
	if syscall.Handle(devs) == syscall.InvalidHandle {
		return ""
	}
	defer procSetupDiDestroyDeviceInfoList.Call(devs)

	for i := uintptr(0); ; i++ {
		var ifd spDeviceInterfaceData
		ifd.CbSize = uint32(unsafe.Sizeof(ifd))
		if ok, _, _ := procSetupDiEnumDeviceInterfaces.Call(devs, 0, uintptr(unsafe.Pointer(&guidDevInterfaceUSBDevice)), i, uintptr(unsafe.Pointer(&ifd))); ok == 0 {
			return ""
		}
		var need uint32
		procSetupDiGetDeviceInterfaceDetailW.Call(devs, uintptr(unsafe.Pointer(&ifd)), 0, 0, uintptr(unsafe.Pointer(&need)), 0)
		if need < 8 {
			continue
		}
		buf := make([]byte, need)
		// cbSize is sizeof(SP_DEVICE_INTERFACE_DETAIL_DATA_W): 8 on 64-bit, 6 on 32-bit.
		cb := uint32(8)
		if unsafe.Sizeof(uintptr(0)) == 4 {
			cb = 6
		}
		*(*uint32)(unsafe.Pointer(&buf[0])) = cb
		if ok, _, _ := procSetupDiGetDeviceInterfaceDetailW.Call(devs, uintptr(unsafe.Pointer(&ifd)), uintptr(unsafe.Pointer(&buf[0])), uintptr(need), 0, 0); ok == 0 {
			continue
		}
		path := syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[4])), (len(buf)-4)/2))
		if strings.Contains(strings.ToLower(path), knobVIDPID) {
			return path
		}
	}
}

// interruptInPipe returns the first interrupt IN endpoint on interface 0.
func interruptInPipe(wu uintptr) (uint8, bool) {
	var id usbInterfaceDescriptor
	if ok, _, _ := procWinUsbQueryInterfaceSettings.Call(wu, 0, uintptr(unsafe.Pointer(&id))); ok == 0 {
		return 0, false
	}
	for i := uint8(0); i < id.BNumEndpoints; i++ {
		var pi winusbPipeInformation
		if ok, _, _ := procWinUsbQueryPipe.Call(wu, 0, uintptr(i), uintptr(unsafe.Pointer(&pi))); ok == 0 {
			continue
		}
		if pi.PipeId&0x80 != 0 && pi.PipeType == usbdPipeInterrupt {
			return pi.PipeId, true
		}
	}
	return 0, false
}

// Read reads one report. It returns errTimeout when nothing arrived within the
// 250 ms pipe timeout.
func (t *winusbTransport) Read(buf []byte) (int, error) {
	var got uint32
	ok, _, e := procWinUsbReadPipe.Call(t.wu, uintptr(t.pipe), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&got)), 0)
	if ok == 0 {
		if errno, isErrno := e.(syscall.Errno); isErrno && errno == errorSemTimeout {
			return 0, errTimeout
		}
		return 0, e
	}
	return int(got), nil
}

// Close aborts any pending read and releases the WinUSB handle and the file.
func (t *winusbTransport) Close() error {
	procWinUsbAbortPipe.Call(t.wu, uintptr(t.pipe))
	procWinUsbFree.Call(t.wu)
	return syscall.CloseHandle(t.handle)
}
