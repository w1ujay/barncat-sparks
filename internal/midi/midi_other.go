//go:build !windows

package midi

import (
	"fmt"
	"sync"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
)

const mimData = 0x3C3 // MIM_DATA (matches midi_windows.go)

// midiEvent carries a raw MIDI message from the callback to the event loop.
type midiEvent struct {
	wMsg   uintptr
	status byte
	data1  byte
	data2  byte
}

// MIDIDriver connects to MIDI devices and dispatches actions to the K4.
type MIDIDriver struct {
	mu        sync.Mutex
	closeOnce sync.Once
	cat       cat.Radio
	config    *MIDIConfig
	index     map[midiKey]*MIDIMapping
	pttOn     bool
	done      chan struct{}
	events    chan midiEvent

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

// ListMIDIDevices is not supported on this platform.
func ListMIDIDevices() error {
	fmt.Println("MIDI device listing is not supported on this platform.")
	fmt.Println("MIDI support requires Windows (winmm.dll).")
	return nil
}

// Run is not supported on non-Windows platforms.
func (d *MIDIDriver) Run() error {
	return fmt.Errorf("MIDI support requires Windows (winmm.dll)")
}

// Close shuts down the MIDI driver.
func (d *MIDIDriver) Close() {
	d.closeOnce.Do(func() {
		close(d.done)
	})
}
