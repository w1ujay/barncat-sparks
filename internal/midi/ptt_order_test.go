package midi

import (
	"sync"
	"testing"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
	"github.com/ujay/barncat-sparks/internal/controls"
)

// pttRadio records PTT and speed calls in the order they reach the radio. A
// non-zero txDelay makes key-down slow. Any other Radio method panics (nil
// embedded interface).
type pttRadio struct {
	cat.Radio
	txDelay time.Duration

	mu     sync.Mutex
	calls  []bool
	speeds []int
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

func (r *pttRadio) SetSpeed(wpm int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.speeds = append(r.speeds, wpm)
	return nil
}

func (r *pttRadio) snapshot() ([]bool, []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.calls...), append([]int(nil), r.speeds...)
}

// pttNote is the note mapped to PTT in these tests; speedCC is the CC mapped
// to keyer speed.
const (
	pttNote = 36
	speedCC = 9
)

func newTestDriver(r cat.Radio) *MIDIDriver {
	return NewMIDIDriver(controls.New(r, false), &MIDIConfig{Mappings: []MIDIMapping{
		{Type: "note_on", Channel: 0, Number: pttNote, Action: "ptt"},
		{Type: "cc", Channel: 0, Number: speedCC, Action: "speed"},
	}})
}

// TestPTTPressAndReleaseReachRadioInOrder: a quick tap must key and then unkey
// the radio, never the reverse, or the radio is left transmitting.
func TestPTTPressAndReleaseReachRadioInOrder(t *testing.T) {
	r := &pttRadio{txDelay: 30 * time.Millisecond}
	d := newTestDriver(r)

	d.handleMIDIEvent(0x90, pttNote, 127) // press
	d.handleMIDIEvent(0x80, pttNote, 0)   // release
	time.Sleep(100 * time.Millisecond)    // let any asynchronous send land

	got, _ := r.snapshot()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("PTT order = %v, want [true false] (TX then RX)", got)
	}
}

// TestNoteOnVelocityZeroReleasesPTT: many controllers send note on with
// velocity 0 instead of note off; it must release PTT the same way.
func TestNoteOnVelocityZeroReleasesPTT(t *testing.T) {
	r := &pttRadio{}
	d := newTestDriver(r)

	d.handleMIDIEvent(0x90, pttNote, 127)
	d.handleMIDIEvent(0x90, pttNote, 0)
	time.Sleep(50 * time.Millisecond)

	got, _ := r.snapshot()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("PTT calls = %v, want [true false]", got)
	}
}

// TestMappedCCReachesExecutor: a mapped CC runs its action with the CC value;
// an unmapped CC runs nothing.
func TestMappedCCReachesExecutor(t *testing.T) {
	r := &pttRadio{}
	d := newTestDriver(r)

	d.handleMIDIEvent(0xB0, speedCC, 127) // speed 5 + 127*94/127 = 99 WPM
	d.handleMIDIEvent(0xB0, 20, 64)       // unmapped CC 20
	time.Sleep(50 * time.Millisecond)     // speed is sent in a goroutine

	ptt, speeds := r.snapshot()
	if len(speeds) != 1 || speeds[0] != 99 {
		t.Errorf("SetSpeed calls = %v, want [99]", speeds)
	}
	if len(ptt) != 0 {
		t.Errorf("PTT calls = %v, want none", ptt)
	}
}
