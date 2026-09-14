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

// MIDIDriver connects to a MIDI device and dispatches actions to the K4.
type MIDIDriver struct {
	mu        sync.Mutex
	closeOnce sync.Once
	cat       cat.Radio
	volumeCat cat.Radio // optional: route volume commands here (e.g. QK4)
	config    *MIDIConfig
	index     map[midiKey]*MIDIMapping
	pttOn     bool
	closed    bool // set by Close under mu; PTT is never keyed after it
	done      chan struct{}
	events    chan midiEvent
	devices   []openDevice // active devices
	cb        uintptr      // Windows callback (reused for reconnect)

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
		select {
		case d.events <- ev:
		default:
		}
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
		case ev := <-d.events:
			switch ev.wMsg {
			case mimClose:
				// Device disconnected — Windows already invalidated the handle.
				d.handleDisconnect(ev.hMidiIn, &disconnected, &ticker, &reconnectCh)
			case mimData:
				d.handleMIDIEvent(ev.status, ev.data1, ev.data2)
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
			d.deviceDisconnected(dev.name)
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

// Close shuts down the MIDI driver and releases all open devices. A PTT that
// MIDI still holds is released first, before the web UI closes the CAT link;
// the pad's own release event will never be processed.
func (d *MIDIDriver) Close() {
	d.closeOnce.Do(func() {
		close(d.done)
		d.releasePTT()
		for _, dev := range d.devices {
			midiInStop.Call(dev.handle)
			midiInReset.Call(dev.handle)
			midiInClose.Call(dev.handle)
		}
	})
}
