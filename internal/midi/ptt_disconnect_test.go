package midi

import (
	"testing"
	"time"
)

// TestDeviceDisconnectReleasesHeldPTT: unplugging the controller while its PTT
// pad is held must unkey the radio, because the pad's release event can never
// arrive.
func TestDeviceDisconnectReleasesHeldPTT(t *testing.T) {
	r := &pttRadio{}
	d := newPTTDriver(r)

	d.handleMIDIEvent(0x90, pttNote, 127) // press and hold
	d.deviceDisconnected("DJ2GO2")
	time.Sleep(50 * time.Millisecond)

	got := r.snapshot()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("PTT calls = %v, want [true false]: a disconnect must release a held PTT", got)
	}
}

// TestPTTKeysAfterDeviceReconnects: a disconnect is not Close. Once the
// controller is plugged back in, its PTT pad must key the radio again.
func TestPTTKeysAfterDeviceReconnects(t *testing.T) {
	r := &pttRadio{}
	d := newPTTDriver(r)

	d.handleMIDIEvent(0x90, pttNote, 127)
	d.deviceDisconnected("DJ2GO2")
	d.handleMIDIEvent(0x90, pttNote, 127) // pressed again after reconnecting
	time.Sleep(50 * time.Millisecond)

	got := r.snapshot()
	if len(got) != 3 || !got[0] || got[1] || !got[2] {
		t.Fatalf("PTT calls = %v, want [true false true]", got)
	}
}

// TestDeviceDisconnectWithoutPTTSendsNothing: a disconnect must not unkey a
// transmission MIDI didn't start.
func TestDeviceDisconnectWithoutPTTSendsNothing(t *testing.T) {
	r := &pttRadio{}
	d := newPTTDriver(r)

	d.deviceDisconnected("DJ2GO2")
	time.Sleep(50 * time.Millisecond)

	if got := r.snapshot(); len(got) != 0 {
		t.Fatalf("PTT calls = %v, want none", got)
	}
}
