package keyer

import (
	"strings"
	"testing"
	"time"
)

// TestRefillAfterXonKeepsEveryCharacter: after XON the host resumes and sends
// until XOFF. Slots sendLoop has already taken must be reusable by then, or
// the refill runs off the end of the buffer and is dropped while XOFF never
// sets.
func TestRefillAfterXonKeepsEveryCharacter(t *testing.T) {
	r := newOrderRadio()
	wk := NewWinkeyer(r)
	wk.hostOpen = true
	tp := newTestPort()
	wk.port = tp
	wk.mu.Lock()
	wk.wpm = 1200 // ~1ms echoes once released
	wk.mu.Unlock()

	const first = "THE QUICK BROWN FOX JUMPS OVER THE LAZY DO" // 42 characters: XOFF
	const refill = "G 0123456789 ABCDEFGHIJK"                  // 24 characters, sent after XON

	for i := 0; i < len(first); i++ {
		wk.processByte(first[i])
	}
	select {
	case <-r.entered: // sendLoop took the first 24 and is parked in SendKY; 18 unsent
	case <-time.After(2 * time.Second):
		t.Fatal("sendLoop never took the first chunk")
	}
	expectStatusByte(t, tp, 0xC4) // busy
	expectStatusByte(t, tp, 0xC5) // 42 queued: XOFF
	expectStatusByte(t, tp, 0xC4) // chunk taken, 18 unsent: XON

	for i := 0; i < len(refill); i++ {
		wk.processByte(refill[i])
	}
	select {
	case b := <-tp.written:
		if b != 0xC5 {
			t.Errorf("after refill, byte to host = 0x%02x, want XOFF status 0xc5 (42 unsent)", b)
		}
	case <-time.After(time.Second):
		t.Errorf("after refill, no status to host, want XOFF 0xc5 (42 unsent)")
	}

	close(r.release)
	want := first + refill
	sent := func() string {
		var b strings.Builder
		for _, c := range r.snapshot() {
			b.WriteString(strings.TrimPrefix(c, "KY "))
		}
		return b.String()
	}
	deadline := time.Now().Add(3 * time.Second)
	for sent() != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := sent(); got != want {
		t.Fatalf("sent %q\nwant %q\ncharacters written after XON were dropped", got, want)
	}
}
