package midi

import (
	"sync"
	"testing"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
)

// pttRadio records SetPTT calls in the order they reach the radio. A non-zero
// txDelay makes key-down slow, like a CAT write that takes a moment. Any other
// Radio method panics (nil embedded interface).
type pttRadio struct {
	cat.Radio
	txDelay time.Duration

	mu    sync.Mutex
	calls []bool
}

func (r *pttRadio) SetPTT(on bool) error {
	if on && r.txDelay > 0 {
		time.Sleep(r.txDelay)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, on)
	return nil
}

func (r *pttRadio) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.calls...)
}

// pttNote is the note mapped to PTT in these tests.
const pttNote = 36

func newPTTDriver(r cat.Radio) *MIDIDriver {
	return NewMIDIDriver(r, &MIDIConfig{Mappings: []MIDIMapping{
		{Type: "note_on", Channel: 0, Number: pttNote, Action: "ptt"},
	}})
}

// TestPTTPressAndReleaseReachRadioInOrder: a quick tap must key and then unkey
// the radio, never the reverse, or the radio is left transmitting.
func TestPTTPressAndReleaseReachRadioInOrder(t *testing.T) {
	r := &pttRadio{txDelay: 30 * time.Millisecond}
	d := newPTTDriver(r)

	d.handleMIDIEvent(0x90, pttNote, 127) // press
	d.handleMIDIEvent(0x80, pttNote, 0)   // release
	time.Sleep(100 * time.Millisecond)    // let any asynchronous send land

	got := r.snapshot()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("PTT order = %v, want [true false] (TX then RX)", got)
	}
}
