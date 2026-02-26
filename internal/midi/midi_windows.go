//go:build windows

package midi

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ujay/barncat-sparks/internal/cat"
)

var (
	winmm             = syscall.NewLazyDLL("winmm.dll")
	midiInGetNumDevs  = winmm.NewProc("midiInGetNumDevs")
	midiInGetDevCapsW = winmm.NewProc("midiInGetDevCapsW")
	midiInOpen        = winmm.NewProc("midiInOpen")
	midiInStart       = winmm.NewProc("midiInStart")
	midiInStop        = winmm.NewProc("midiInStop")
	midiInReset       = winmm.NewProc("midiInReset")
	midiInClose       = winmm.NewProc("midiInClose")
)

const (
	callbackFunction = 0x00030000 // CALLBACK_FUNCTION
	mimOpen          = 0x3C1      // MIM_OPEN
	mimClose         = 0x3C2      // MIM_CLOSE
	mimData          = 0x3C3      // MIM_DATA
	mimLongData      = 0x3C4      // MIM_LONGDATA
	mimError         = 0x3C5      // MIM_ERROR
)

// midiInCapsW matches the Windows MIDIINCAPSW structure.
type midiInCapsW struct {
	wMid           uint16
	wPid           uint16
	vDriverVersion uint32
	szPname        [32]uint16 // MAXPNAMELEN = 32
	dwSupport      uint32
}

// midiEvent carries a raw MIDI message from the callback to the event loop.
type midiEvent struct {
	wMsg   uintptr // Windows message type (MIM_DATA, MIM_OPEN, etc.)
	status byte
	data1  byte
	data2  byte
}

// MIDIDriver connects to a MIDI device and dispatches actions to the K4.
type MIDIDriver struct {
	mu        sync.Mutex
	closeOnce sync.Once
	cat       cat.Radio
	config    *MIDIConfig
	index     map[midiKey]*MIDIMapping
	pttOn     bool
	done      chan struct{}
	events    chan midiEvent
	hMidiIns  []uintptr

	// VFO tuning state (accessed only from event loop goroutine)
	vfoFreq     int
	vfoKnown    bool
	lastTuneAt  time.Time
	vfoBFreq    int
	vfoBKnown   bool
	lastTuneBAt time.Time

	// Toggle state for button actions (accessed only from event loop goroutine)
	ritOn    bool
	xitOn    bool
	nbOn     bool
	nrOn     bool
	splitOn  bool
	notchOn  bool
	notchHz  int // current notch frequency in Hz (0 = unknown, default 1000)
	filterBW int // current filter BW in Hz (0 = unknown, default 500)

	// RF gain state (accessed only from event loop goroutine)
	rfGain  int // main RX attenuation level (0-60, 0=full gain)
	rfGainB int // sub RX attenuation level
}

// NewMIDIDriver creates a new MIDI driver.
func NewMIDIDriver(c cat.Radio, config *MIDIConfig) *MIDIDriver {
	return &MIDIDriver{
		cat:    c,
		config: config,
		index:  buildMappingIndex(config),
		done:   make(chan struct{}),
		events: make(chan midiEvent, 64),
	}
}

// ListMIDIDevices prints available MIDI input devices to stdout.
func ListMIDIDevices() error {
	count, _, _ := midiInGetNumDevs.Call()
	if count == 0 {
		fmt.Println("No MIDI input devices found.")
		return nil
	}
	fmt.Printf("MIDI input devices (%d):\n", count)
	for i := uintptr(0); i < count; i++ {
		var caps midiInCapsW
		ret, _, _ := midiInGetDevCapsW.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
		if ret != 0 {
			continue
		}
		name := syscall.UTF16ToString(caps.szPname[:])
		fmt.Printf("  %d: %s\n", i, name)
	}
	return nil
}

// Run opens all configured MIDI devices and starts listening.
// All devices share the same event channel and dispatch loop.
// Blocks until Close is called.
func (d *MIDIDriver) Run() error {
	devices := d.config.GetDevices()
	if len(devices) == 0 {
		return fmt.Errorf("no MIDI devices configured")
	}

	count, _, _ := midiInGetNumDevs.Call()
	if count == 0 {
		return fmt.Errorf("no MIDI input devices found")
	}

	cb := syscall.NewCallback(func(hMidiIn, wMsg, dwInstance, dwParam1, dwParam2 uintptr) uintptr {
		ev := midiEvent{wMsg: wMsg}
		if wMsg == mimData {
			ev.status = byte(dwParam1 & 0xFF)
			ev.data1 = byte((dwParam1 >> 8) & 0xFF)
			ev.data2 = byte((dwParam1 >> 16) & 0xFF)
		}
		select {
		case d.events <- ev:
		default:
		}
		return 0
	})

	for _, deviceName := range devices {
		portIdx := d.findDevice(deviceName, count)
		if portIdx < 0 {
			log.Printf("MIDI: device %q not found, skipping", deviceName)
			continue
		}

		var hMidiIn uintptr
		ret, _, _ := midiInOpen.Call(
			uintptr(unsafe.Pointer(&hMidiIn)),
			uintptr(portIdx),
			cb,
			0,
			callbackFunction,
		)
		if ret != 0 {
			log.Printf("MIDI: midiInOpen failed for %q (MMRESULT=%d)", deviceName, ret)
			continue
		}

		ret, _, _ = midiInStart.Call(hMidiIn)
		if ret != 0 {
			midiInClose.Call(hMidiIn)
			log.Printf("MIDI: midiInStart failed for %q (MMRESULT=%d)", deviceName, ret)
			continue
		}

		d.hMidiIns = append(d.hMidiIns, hMidiIn)
		log.Printf("MIDI: listening on %q (port %d)", deviceName, portIdx)
	}

	if len(d.hMidiIns) == 0 {
		return fmt.Errorf("no MIDI devices could be opened")
	}

	for {
		select {
		case ev := <-d.events:
			if ev.wMsg != mimData {
				continue
			}
			d.handleMIDIEvent(ev.status, ev.data1, ev.data2)
		case <-d.done:
			return nil
		}
	}
}

// findDevice searches system MIDI devices for a name matching the substring.
func (d *MIDIDriver) findDevice(name string, count uintptr) int {
	for i := uintptr(0); i < count; i++ {
		var caps midiInCapsW
		ret, _, _ := midiInGetDevCapsW.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
		if ret != 0 {
			continue
		}
		devName := syscall.UTF16ToString(caps.szPname[:])
		if strings.Contains(strings.ToLower(devName), strings.ToLower(name)) {
			log.Printf("MIDI: matched %q at port %d", devName, i)
			return int(i)
		}
	}
	return -1
}

// Close shuts down the MIDI driver and releases all open devices.
func (d *MIDIDriver) Close() {
	d.closeOnce.Do(func() {
		for _, h := range d.hMidiIns {
			midiInStop.Call(h)
			midiInReset.Call(h)
			midiInClose.Call(h)
		}
		close(d.done)
	})
}
