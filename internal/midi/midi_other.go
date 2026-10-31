//go:build !windows

package midi

import (
	"context"
	"fmt"
	"sync"

	"github.com/ujay/barncat-sparks/internal/controls"
)

const mimData = 0x3C3 // MIM_DATA (matches midi_windows.go)

// midiEvent carries a raw MIDI message from the callback to the event loop.
type midiEvent struct {
	wMsg   uintptr
	status byte
	data1  byte
	data2  byte
}

// MIDIDriver connects to MIDI devices and hands their messages to an executor.
type MIDIDriver struct {
	closeOnce sync.Once
	exec      *controls.Executor
	config    *MIDIConfig
	index     map[midiKey]*MIDIMapping
	done      chan struct{}
	events    *eventQueue
	learn     learner
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

// ListMIDIDevices is not supported on this platform.
func ListMIDIDevices() error {
	fmt.Println("MIDI device listing is not supported on this platform.")
	fmt.Println("MIDI support requires Windows (winmm.dll).")
	return nil
}

// LearnOnce is not supported on this platform.
func LearnOnce(ctx context.Context, devices []string) (Learned, error) {
	return Learned{}, ErrUnsupported
}

// Run is not supported on non-Windows platforms.
func (d *MIDIDriver) Run() error {
	return fmt.Errorf("MIDI support requires Windows (winmm.dll)")
}

// Close shuts down the MIDI driver. Releasing a held PTT is the executor's job.
func (d *MIDIDriver) Close() {
	d.closeOnce.Do(func() {
		close(d.done)
	})
}
