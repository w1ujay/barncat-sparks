package keyer

import (
	"testing"
	"time"
)

// Close releases everything the keyer holds, but Run may still hand
// processByte a byte read before the port finished closing. None of it may
// reach the radio after Close.

// TestKeyImmediateParamAfterCloseDoesNotKey: a key immediate whose on byte is
// still being parsed when Close runs must not key the radio afterwards.
func TestKeyImmediateParamAfterCloseDoesNotKey(t *testing.T) {
	r := &tuneRadio{}
	wk := NewWinkeyer(r)
	wk.hostOpen = true
	wk.port = newTestPort()

	wk.processByte(0x0B) // key immediate; its on/off byte hasn't arrived yet
	wk.Close()
	wk.processByte(0x01) // on
	time.Sleep(50 * time.Millisecond)

	if got := r.snapshot(); len(got) != 0 {
		t.Fatalf("PTT calls after Close = %v, want none", got)
	}
}

// TestCWAfterCloseDoesNotSend: text read from the port after Close must not
// start a sendLoop that keys the radio.
func TestCWAfterCloseDoesNotSend(t *testing.T) {
	r := newOrderRadio()
	close(r.release) // SendKY returns immediately
	wk := NewWinkeyer(r)
	wk.hostOpen = true
	wk.port = newTestPort()

	wk.Close()
	wk.processByte('E')
	time.Sleep(250 * time.Millisecond) // past sendLoop's 120ms coalescing delay

	if got := r.snapshot(); len(got) != 0 {
		t.Fatalf("radio commands after Close = %v, want none", got)
	}
}

// TestRTTYBracketAfterCloseDoesNotKey: an RTTY [ read after Close must not
// key PTT.
func TestRTTYBracketAfterCloseDoesNotKey(t *testing.T) {
	r := &tuneRadio{}
	wk := NewWinkeyer(r)
	wk.hostOpen = true
	wk.port = newTestPort()

	wk.Close()
	wk.processByte('[')
	time.Sleep(50 * time.Millisecond)

	if got := r.snapshot(); len(got) != 0 {
		t.Fatalf("PTT calls after Close = %v, want none", got)
	}
}
