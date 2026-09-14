package keyer

import (
	"testing"
	"time"
)

// WK3 reports a status byte whenever status changes. The host pauses when XOFF
// sets and resumes when it clears; if neither is reported, it keeps sending
// into a full buffer (characters past 64 are dropped) or waits for idle.

// expectStatusByte reads the next byte written to the host and checks it is
// exactly want.
func expectStatusByte(t *testing.T, p *testPort, want byte) {
	t.Helper()
	select {
	case b := <-p.written:
		if b != want {
			t.Fatalf("byte to host = 0x%02x, want status 0x%02x", b, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("no byte to host, want status 0x%02x", want)
	}
}

// fillToXoff queues 42 characters (the 2/3-full threshold) at 5 WPM, faster
// than sendLoop's 120ms coalescing delay, so none has been taken yet.
func fillToXoff(t *testing.T) (*Winkeyer, *testPort) {
	t.Helper()
	wk, tp, _ := newTestWK(t)
	wk.mu.Lock()
	wk.wpm = 5
	wk.mu.Unlock()
	for i := 0; i < 42; i++ {
		wk.processByte('E')
	}
	return wk, tp
}

// TestBufferFillingToXoffReportsStatus: reaching the XOFF threshold while busy
// must send a status byte with XOFF set.
func TestBufferFillingToXoffReportsStatus(t *testing.T) {
	_, tp := fillToXoff(t)

	expectStatusByte(t, tp, 0xC4) // first character: busy
	expectStatusByte(t, tp, 0xC5) // 42 queued: busy + XOFF
}

// TestXoffClearReportsStatus: once sendLoop takes a 24-character chunk, 18
// remain (below 20), and the host must be told XOFF cleared before the first
// echo, not only when the whole buffer drains.
func TestXoffClearReportsStatus(t *testing.T) {
	_, tp := fillToXoff(t)

	expectStatusByte(t, tp, 0xC4) // busy
	expectStatusByte(t, tp, 0xC5) // busy + XOFF
	expectStatusByte(t, tp, 0xC4) // chunk taken: busy, XOFF clear
}
