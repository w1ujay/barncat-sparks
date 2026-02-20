//go:build !windows

package main

import (
	"fmt"
	"sync"
	"time"
)

// MIDIDriver connects to a MIDI device and dispatches actions to the K4.
type MIDIDriver struct {
	mu     sync.Mutex
	cat    *CATClient
	config *MIDIConfig
	index  map[midiKey]*MIDIMapping
	pttOn  bool
	done   chan struct{}

	// VFO tuning state (accessed only from event loop goroutine)
	vfoFreq    int
	vfoKnown   bool
	lastTuneAt time.Time
}

// NewMIDIDriver creates a new MIDI driver.
func NewMIDIDriver(cat *CATClient, config *MIDIConfig) *MIDIDriver {
	return &MIDIDriver{
		cat:    cat,
		config: config,
		index:  buildMappingIndex(config),
		done:   make(chan struct{}),
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
	close(d.done)
}
