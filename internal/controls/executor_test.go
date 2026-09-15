package controls

import (
	"sync"
	"testing"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
)

// fakeRadio records PTT and VFO A calls. Any other Radio method panics
// (nil embedded interface), so a test fails loudly on an unexpected call.
type fakeRadio struct {
	cat.Radio
	txDelay time.Duration

	mu       sync.Mutex
	ptt      []bool
	freqA    int
	getFreqA int
	setFreqA []int
}

func (r *fakeRadio) SetPTT(on bool) error {
	if on && r.txDelay > 0 {
		time.Sleep(r.txDelay)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ptt = append(r.ptt, on)
	return nil
}

func (r *fakeRadio) GetFreqA() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getFreqA++
	return r.freqA, nil
}

func (r *fakeRadio) SetFreqA(hz int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setFreqA = append(r.setFreqA, hz)
	return nil
}

func (r *fakeRadio) snapshot() (ptt []bool, gets int, sets []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.ptt...), r.getFreqA, append([]int(nil), r.setFreqA...)
}

var (
	pttPress   = Event{Action: "ptt", Value: 127}
	pttRelease = Event{Action: "ptt", Release: true}
)

func wantPTT(t *testing.T, r *fakeRadio, want []bool) {
	t.Helper()
	time.Sleep(50 * time.Millisecond) // let any asynchronous send land
	got, _, _ := r.snapshot()
	if len(got) != len(want) {
		t.Fatalf("PTT calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PTT calls = %v, want %v", got, want)
		}
	}
}

// TestPTTPressAndReleaseReachRadioInOrder: a quick tap must key and then
// unkey the radio, never the reverse, or the radio is left transmitting.
func TestPTTPressAndReleaseReachRadioInOrder(t *testing.T) {
	r := &fakeRadio{txDelay: 30 * time.Millisecond}
	e := New(r, false)
	e.Handle(pttPress)
	e.Handle(pttRelease)
	wantPTT(t, r, []bool{true, false})
}

// TestCloseReleasesHeldPTT: stopping while a PTT is held must unkey the radio.
func TestCloseReleasesHeldPTT(t *testing.T) {
	r := &fakeRadio{}
	e := New(r, false)
	e.Handle(pttPress)
	e.Close()
	wantPTT(t, r, []bool{true, false})
}

// TestPressAfterCloseDoesNotKey: an event still arriving during shutdown must
// not key the radio after Close released it.
func TestPressAfterCloseDoesNotKey(t *testing.T) {
	r := &fakeRadio{}
	e := New(r, false)
	e.Close()
	e.Handle(pttPress)
	wantPTT(t, r, []bool{})
}

// TestCloseWithoutPTTSendsNothing: Close must not unkey a transmission the
// executor didn't start.
func TestCloseWithoutPTTSendsNothing(t *testing.T) {
	r := &fakeRadio{}
	New(r, false).Close()
	wantPTT(t, r, []bool{})
}

// TestCloseAfterReleaseSendsNothingMore: a released PTT isn't released twice.
func TestCloseAfterReleaseSendsNothingMore(t *testing.T) {
	r := &fakeRadio{}
	e := New(r, false)
	e.Handle(pttPress)
	e.Handle(pttRelease)
	e.Close()
	e.Close() // idempotent
	wantPTT(t, r, []bool{true, false})
}

// TestDeviceLostReleasesHeldPTT: a controller unplugged with PTT held must
// unkey the radio, and PTT keys again once it's back.
func TestDeviceLostReleasesHeldPTT(t *testing.T) {
	r := &fakeRadio{}
	e := New(r, false)
	e.Handle(pttPress)
	e.DeviceLost("DJ2GO2")
	e.Handle(pttPress) // pressed again after reconnecting
	wantPTT(t, r, []bool{true, false, true})
}

// TestDeviceLostWithoutPTTSendsNothing: a disconnect must not unkey a
// transmission the executor didn't start.
func TestDeviceLostWithoutPTTSendsNothing(t *testing.T) {
	r := &fakeRadio{}
	New(r, false).DeviceLost("DJ2GO2")
	wantPTT(t, r, []bool{})
}

// TestRelativeTuneSharesOneCache: value 1 tunes up one step and 127 tunes down
// one step, and events from different inputs share one VFO cache and one
// throttle, so the radio is queried once and never flooded.
func TestRelativeTuneSharesOneCache(t *testing.T) {
	r := &fakeRadio{freqA: 14074000}
	e := New(r, false)
	step := map[string]any{"step": float64(10)}

	e.Handle(Event{Action: "vfo_tune", Opts: step, Value: 1})   // e.g. a DJ2GO2 jog: query, then 14074010
	e.Handle(Event{Action: "vfo_tune", Opts: step, Value: 127}) // e.g. the knob: cached, back to 14074000, throttled
	time.Sleep(60 * time.Millisecond)                           // past the 50 ms throttle, inside the 250 ms cache
	e.Handle(Event{Action: "vfo_tune", Opts: step, Value: 1})   // cached: 14074010, sent

	_, gets, sets := r.snapshot()
	if gets != 1 {
		t.Errorf("GetFreqA called %d times, want 1 (the cache is shared)", gets)
	}
	if len(sets) != 2 || sets[0] != 14074010 || sets[1] != 14074010 {
		t.Errorf("SetFreqA calls = %v, want [14074010 14074010]", sets)
	}
}
