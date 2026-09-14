package midi

import (
	"testing"
	"time"
)

// TestCloseReleasesHeldPTT: stopping MIDI while a PTT pad is held must unkey
// the radio, because the pad's release event will never be processed.
func TestCloseReleasesHeldPTT(t *testing.T) {
	r := &pttRadio{}
	d := newPTTDriver(r)

	d.handleMIDIEvent(0x90, pttNote, 127) // press and hold
	d.Close()
	time.Sleep(50 * time.Millisecond)

	got := r.snapshot()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("PTT calls = %v, want [true false]: Close must release a held PTT", got)
	}
}

// TestPressAfterCloseDoesNotKey: an event the loop is still finishing when
// Close runs must not key the radio after Close released it.
func TestPressAfterCloseDoesNotKey(t *testing.T) {
	r := &pttRadio{}
	d := newPTTDriver(r)

	d.Close()
	d.handleMIDIEvent(0x90, pttNote, 127) // a press that arrived during shutdown
	time.Sleep(50 * time.Millisecond)

	if got := r.snapshot(); len(got) != 0 {
		t.Fatalf("PTT calls = %v, want none after Close", got)
	}
}

// TestCloseWithoutPTTSendsNothing: Close must not unkey a transmission MIDI
// didn't start.
func TestCloseWithoutPTTSendsNothing(t *testing.T) {
	r := &pttRadio{}
	d := newPTTDriver(r)

	d.Close()
	time.Sleep(50 * time.Millisecond)

	if got := r.snapshot(); len(got) != 0 {
		t.Fatalf("PTT calls = %v, want none", got)
	}
}

// TestCloseAfterReleaseSendsNothingMore: a PTT that was already released must
// not be released a second time on Close.
func TestCloseAfterReleaseSendsNothingMore(t *testing.T) {
	r := &pttRadio{}
	d := newPTTDriver(r)

	d.handleMIDIEvent(0x90, pttNote, 127)
	d.handleMIDIEvent(0x80, pttNote, 0)
	d.Close()
	time.Sleep(50 * time.Millisecond)

	got := r.snapshot()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("PTT calls = %v, want [true false] with no extra release", got)
	}
}
