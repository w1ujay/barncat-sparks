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

	"github.com/ujay/barncat-sparks/internal/controls"
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

	reconnectInterval = 3 * time.Second
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
	hMidiIn uintptr // handle that generated this event
	wMsg    uintptr // Windows message type (MIM_DATA, MIM_OPEN, etc.)
	status  byte
	data1   byte
	data2   byte
}

// openDevice tracks a MIDI device handle and its configured name.
type openDevice struct {
	name   string
	handle uintptr
}

// MIDIDriver connects to MIDI devices and hands their messages to an executor.
type MIDIDriver struct {
	closeOnce sync.Once
	exec      *controls.Executor
	config    *MIDIConfig
	index     map[midiKey]*MIDIMapping
	done      chan struct{}
	events    *eventQueue
	devices   []openDevice // active devices
	cb        uintptr      // Windows callback (reused for reconnect)
}

// NewMIDIDriver creates a new MIDI driver that runs mapped actions on exec.
func NewMIDIDriver(exec *controls.Executor, config *MIDIConfig) *MIDIDriver {
	return &MIDIDriver{
		exec:   exec,
		config: config,
		index:  buildMappingIndex(config),
		done:   make(chan struct{}),
		events: newEventQueue(64, 4096),
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
// Automatically reconnects devices that disconnect (USB unplug/replug).
// Blocks until Close is called.
func (d *MIDIDriver) Run() error {
	configuredDevices := d.config.GetDevices()
	if len(configuredDevices) == 0 {
		return fmt.Errorf("no MIDI devices configured")
	}

	d.cb = syscall.NewCallback(func(hMidiIn, wMsg, dwInstance, dwParam1, dwParam2 uintptr) uintptr {
		ev := midiEvent{hMidiIn: hMidiIn, wMsg: wMsg}
		if wMsg == mimData {
			ev.status = byte(dwParam1 & 0xFF)
			ev.data1 = byte((dwParam1 >> 8) & 0xFF)
			ev.data2 = byte((dwParam1 >> 16) & 0xFF)
		}
		// Only Control Change (jogs, knobs) may be dropped when the loop
		// falls behind; notes carry PTT release and MIM_CLOSE a disconnect.
		d.events.push(ev, wMsg == mimData && ev.status&0xF0 == 0xB0)
		return 0
	})

	// Initial open — try all configured devices.
	var disconnected []string
	for _, name := range configuredDevices {
		if !d.openDevice(name) {
			disconnected = append(disconnected, name)
		}
	}

	// Start reconnect ticker if any devices didn't open.
	var ticker *time.Ticker
	var reconnectCh <-chan time.Time
	if len(disconnected) > 0 {
		ticker = time.NewTicker(reconnectInterval)
		reconnectCh = ticker.C
	}

	if len(d.devices) == 0 && len(disconnected) == 0 {
		return fmt.Errorf("no MIDI devices could be opened")
	}
	if len(d.devices) == 0 {
		// None opened but some are in reconnect list — start reconnect loop.
		log.Printf("MIDI: no devices available, will retry every %v", reconnectInterval)
	}

	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	for {
		select {
		case <-d.events.ready:
			evs, lost := d.events.drain()
			for _, ev := range evs {
				switch ev.wMsg {
				case mimClose:
					// Device disconnected — Windows already invalidated the handle.
					d.handleDisconnect(ev.hMidiIn, &disconnected, &ticker, &reconnectCh)
				case mimData:
					d.handleMIDIEvent(ev.status, ev.data1, ev.data2)
				}
			}
			if lost {
				// A dropped message may have been a PTT release.
				d.exec.ReleasePTT("MIDI event queue overflowed")
			}

		case <-reconnectCh:
			d.tryReconnect(&disconnected, &ticker, &reconnectCh)

		case <-d.done:
			return nil
		}
	}
}

// openDevice tries to open a MIDI device by name. Returns true if successful.
func (d *MIDIDriver) openDevice(name string) bool {
	count, _, _ := midiInGetNumDevs.Call()
	if count == 0 {
		return false
	}

	portIdx := d.findDevice(name, count)
	if portIdx < 0 {
		log.Printf("MIDI: device %q not found", name)
		return false
	}

	var hMidiIn uintptr
	ret, _, _ := midiInOpen.Call(
		uintptr(unsafe.Pointer(&hMidiIn)),
		uintptr(portIdx),
		d.cb,
		0,
		callbackFunction,
	)
	if ret != 0 {
		log.Printf("MIDI: midiInOpen failed for %q (MMRESULT=%d)", name, ret)
		return false
	}

	ret, _, _ = midiInStart.Call(hMidiIn)
	if ret != 0 {
		midiInClose.Call(hMidiIn)
		log.Printf("MIDI: midiInStart failed for %q (MMRESULT=%d)", name, ret)
		return false
	}

	d.devices = append(d.devices, openDevice{name: name, handle: hMidiIn})
	log.Printf("MIDI: listening on %q (port %d)", name, portIdx)
	return true
}

// handleDisconnect processes a MIM_CLOSE event for a device.
func (d *MIDIDriver) handleDisconnect(handle uintptr, disconnected *[]string, ticker **time.Ticker, reconnectCh *<-chan time.Time) {
	for i, dev := range d.devices {
		if dev.handle == handle {
			log.Printf("MIDI: device %q disconnected", dev.name)
			d.exec.DeviceLost(dev.name)
			*disconnected = append(*disconnected, dev.name)
			d.devices = append(d.devices[:i], d.devices[i+1:]...)
			// Start reconnect ticker if not already running.
			if *ticker == nil {
				*ticker = time.NewTicker(reconnectInterval)
				*reconnectCh = (*ticker).C
			}
			return
		}
	}
	log.Printf("MIDI: unknown device disconnected (handle=%d)", handle)
}

// tryReconnect attempts to reopen disconnected devices.
func (d *MIDIDriver) tryReconnect(disconnected *[]string, ticker **time.Ticker, reconnectCh *<-chan time.Time) {
	remaining := (*disconnected)[:0]
	for _, name := range *disconnected {
		if d.openDevice(name) {
			log.Printf("MIDI: device %q reconnected", name)
		} else {
			remaining = append(remaining, name)
		}
	}
	*disconnected = remaining

	// Stop ticker if all devices are reconnected.
	if len(*disconnected) == 0 && *ticker != nil {
		(*ticker).Stop()
		*ticker = nil
		*reconnectCh = nil
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
			return int(i)
		}
	}
	return -1
}

// Close shuts down the MIDI driver and releases all open devices. Releasing a
// held PTT is the executor's job; close the executor after the driver.
func (d *MIDIDriver) Close() {
	d.closeOnce.Do(func() {
		close(d.done)
		for _, dev := range d.devices {
			midiInStop.Call(dev.handle)
			midiInReset.Call(dev.handle)
			midiInClose.Call(dev.handle)
		}
	})
}
