package keyer

import (
	"sync"
	"testing"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
)

// orderRadio records the CW and PTT commands the keyer sends, in order.
// SendKY blocks until release is closed, holding sendLoop inside the window
// between its cancel check and the wire write. Any other Radio method panics
// (nil embedded interface), so a test fails loudly if the keyer calls one.
type orderRadio struct {
	cat.Radio
	entered chan struct{} // receives once when SendKY is first entered
	release chan struct{} // close to let SendKY return

	mu    sync.Mutex
	calls []string
}

func newOrderRadio() *orderRadio {
	return &orderRadio{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (r *orderRadio) record(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *orderRadio) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *orderRadio) SendKY(text string) error {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	<-r.release
	r.record("KY " + text)
	return nil
}

func (r *orderRadio) SendAbort() error {
	r.record("RX")
	return nil
}

func (r *orderRadio) SetPTT(on bool) error {
	if on {
		r.record("TX")
	} else {
		r.record("RX")
	}
	return nil
}

// newOrderWK returns a keyer whose sendLoop is parked inside SendKY("E"):
// past its cancel check, before the command reaches the wire.
func newOrderWK(t *testing.T) (*Winkeyer, *orderRadio) {
	t.Helper()
	r := newOrderRadio()
	wk := NewWinkeyer(r)
	wk.hostOpen = true
	wk.port = newTestPort()
	wk.processByte('E')
	select {
	case <-r.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("sendLoop never reached SendKY")
	}
	return wk, r
}

// expectKYBeforeAbort runs abort while the KY is in flight, releases the KY,
// and checks that the abort reached the radio after it.
func expectKYBeforeAbort(t *testing.T, r *orderRadio, abort func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		abort()
		close(done)
	}()
	time.Sleep(50 * time.Millisecond) // an unguarded abort goes out now, ahead of the KY
	close(r.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("abort did not return after SendKY was released")
	}
	time.Sleep(50 * time.Millisecond) // let a late KY land if the order is wrong
	got := r.snapshot()
	if len(got) != 2 || got[0] != "KY E" || got[1] != "RX" {
		t.Fatalf("wire order = %v, want [KY E RX]: the abort must follow any KY already in flight", got)
	}
}

// TestCloseAbortComesAfterInFlightKY: Stop must not let a KY that sendLoop
// already committed to reach the radio after Close's RX.
func TestCloseAbortComesAfterInFlightKY(t *testing.T) {
	wk, r := newOrderWK(t)
	expectKYBeforeAbort(t, r, wk.Close)
}
