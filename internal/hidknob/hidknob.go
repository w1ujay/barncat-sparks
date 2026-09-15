// Package hidknob reads the DROK/iWit USB knob (USB ID 0483:5742) after its
// Windows driver has been replaced with WinUSB, and turns its consumer-control
// reports into executor events.
package hidknob

import (
	"errors"

	"github.com/ujay/barncat-sparks/internal/controls"
)

// State is the knob's connection state.
type State string

const (
	StateConnected    State = "connected"
	StateNotSetUp     State = "not_set_up"    // present, but still on the Windows HID driver
	StateNotConnected State = "not_connected" // not plugged in
	StateUnsupported  State = "unsupported"   // this platform can't read it
)

var (
	ErrNotSetUp     = errors.New("knob found but not set up: replace its driver with WinUSB using Zadig")
	ErrNotConnected = errors.New("knob not connected")
	ErrUnsupported  = errors.New("the knob requires Windows")
	ErrBusy         = errors.New("already learning")

	// errTimeout is what a transport returns when nothing arrived within its
	// read timeout. It is not a failure.
	errTimeout = errors.New("hidknob: read timeout")
)

// consumerReportID is the report carrying one 16-bit consumer usage.
const consumerReportID = 3

// Mapping is one Knob row: a usage code and the action it runs.
type Mapping struct {
	Usage     uint16
	Action    string
	Opts      map[string]any
	Direction int // +1 or -1 for relative actions; 0 for button actions
}

// Sink runs events; *controls.Executor is the real one.
type Sink interface {
	Handle(controls.Event)
}

// DecodeReport returns the usage in a report-3 packet. ok is false for a
// release (usage 0), any other report ID, and a packet shorter than 3 bytes.
func DecodeReport(b []byte) (uint16, bool) {
	if len(b) < 3 || b[0] != consumerReportID {
		return 0, false
	}
	usage := uint16(b[1]) | uint16(b[2])<<8
	return usage, usage != 0
}

// eventFor builds the executor event for a mapping. Relative actions take a
// two's-complement step: +1 is value 1, -1 is value 127. Button actions ignore
// the value; 127 is a full press.
func eventFor(m Mapping) controls.Event {
	value := 127
	if m.Direction > 0 {
		value = 1
	}
	return controls.Event{Action: m.Action, Opts: m.Opts, Value: value}
}
