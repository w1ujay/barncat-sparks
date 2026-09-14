package keyer

import (
	"strings"
	"testing"
	"time"
)

// TestLateOverwriteKeepsPointerMoving: a correction that starts at a position
// sendLoop has already taken can't change what was sent, but the pointer must
// still advance past it, so the rest of the correction lands on unsent
// positions and input keeps working after the message drains.
func TestLateOverwriteKeepsPointerMoving(t *testing.T) {
	r := newOrderRadio()
	wk := NewWinkeyer(r)
	wk.hostOpen = true
	tp := newTestPort()
	wk.port = tp
	wk.mu.Lock()
	wk.wpm = 1200 // ~1ms echoes once released
	wk.mu.Unlock()

	const msg = "ABCDEFGHIJKLMNOPQRSTUVWXYZ" // 26 characters
	for i := 0; i < len(msg); i++ {
		wk.processByte(msg[i])
	}
	select {
	case <-r.entered: // sendLoop took positions 0-23; "YZ" at 24-25 is unsent
	case <-time.After(2 * time.Second):
		t.Fatal("sendLoop never took the first chunk")
	}

	// Overwrite from position 23 (already sent) with "123": 1 is too late,
	// 2 and 3 replace the unsent Y and Z.
	for _, b := range []byte{0x16, 0x01, 23, '1', '2', '3'} {
		wk.processByte(b)
	}
	close(r.release)
	waitIdle(t, tp)

	wk.processByte('4') // new input after the message drained

	want := "ABCDEFGHIJKLMNOPQRSTUVWX" + "23" + "4"
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
		t.Fatalf("sent %q\nwant %q\na late overwrite must skip taken positions without stalling the pointer", got, want)
	}
}
