package keyer

import (
	"testing"
	"time"
)

// TestUnpauseRestartsQueuedCW: CW buffered while the host has the keyer paused
// must start sending as soon as it unpauses, without waiting for more input.
func TestUnpauseRestartsQueuedCW(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	wk.processByte(0x06) // pause
	wk.processByte(0x01) // on
	wk.processByte('E')
	time.Sleep(300 * time.Millisecond) // sendLoop starts, sees the pause, and stops

	select {
	case cmd := <-cmds:
		t.Fatalf("sent %q while paused", cmd)
	default:
	}
	drainPort(tp, 50*time.Millisecond)

	wk.processByte(0x06) // pause
	wk.processByte(0x00) // off
	expectCmdTimeout(t, cmds, "KY E", 2*time.Second)
}
