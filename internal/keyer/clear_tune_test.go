package keyer

import (
	"testing"
	"time"
)

// TestClearBufferStopsTune: N1MM's Esc (clear buffer) during a tune carrier
// must unkey the radio.
func TestClearBufferStopsTune(t *testing.T) {
	wk, _, cmds := newTestWK(t)

	wk.processByte(0x0B) // key immediate
	wk.processByte(0x01) // tune on
	expectCmd(t, cmds, "TX")

	wk.processByte(0x0A) // clear buffer
	expectCmdTimeout(t, cmds, "RX", 500*time.Millisecond)

	wk.mu.Lock()
	tuning := wk.tuning
	wk.mu.Unlock()
	if tuning {
		t.Error("tuning still set after clear buffer")
	}
}
