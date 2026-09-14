package keyer

import (
	"sync"
	"testing"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
)

// tuneRadio records SetPTT calls in the order they reach the radio, with a
// slow key-down. Any other Radio method panics (nil embedded interface).
type tuneRadio struct {
	cat.Radio
	txDelay time.Duration

	mu    sync.Mutex
	calls []bool
}

func (r *tuneRadio) SetPTT(on bool) error {
	if on && r.txDelay > 0 {
		time.Sleep(r.txDelay)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, on)
	return nil
}

func (r *tuneRadio) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.calls...)
}

// TestKeyImmediateOnOffReachRadioInOrder: a quick tune on/off from the logger
// must key and then unkey the radio, never the reverse.
func TestKeyImmediateOnOffReachRadioInOrder(t *testing.T) {
	r := &tuneRadio{txDelay: 30 * time.Millisecond}
	wk := NewWinkeyer(r)
	wk.hostOpen = true
	wk.port = newTestPort()

	wk.processByte(0x0B) // key immediate
	wk.processByte(0x01) // on
	wk.processByte(0x0B)
	wk.processByte(0x00)               // off
	time.Sleep(150 * time.Millisecond) // let any asynchronous send land

	got := r.snapshot()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("tune PTT order = %v, want [true false] (TX then RX)", got)
	}
}
