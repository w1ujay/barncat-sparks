package keyer

import (
	"testing"
	"time"
)

// pendingRTTY sends one RTTY message and returns while its delayed PTT release
// is still pending, with the radio still transmitting.
func pendingRTTY(t *testing.T) (*Winkeyer, chan string) {
	t.Helper()
	wk, tp, cmds := newTestWK(t)
	wk.processByte(0x00) // admin
	wk.processByte(0x13) // FSK mode
	wk.processByte(0x80) // enable
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	wk.processByte('[')
	expectCmd(t, cmds, "TX")
	wk.processByte('E')
	wk.processByte(']')
	expectCmd(t, cmds, "KY E")

	wk.mu.Lock()
	pending := wk.rttyDoneCancel != nil
	wk.mu.Unlock()
	if !pending {
		t.Fatal("expected the ] PTT release to still be pending")
	}
	return wk, cmds
}

// TestResetReleasesPendingRTTYPTT: an admin reset between ] and its delayed RX
// must unkey the radio, not just cancel the release.
func TestResetReleasesPendingRTTYPTT(t *testing.T) {
	wk, cmds := pendingRTTY(t)

	wk.processByte(0x00)
	wk.processByte(0x01) // admin reset
	expectCmdTimeout(t, cmds, "RX", 300*time.Millisecond)
}

// TestHostCloseReleasesPendingRTTYPTT: the same for host close.
func TestHostCloseReleasesPendingRTTYPTT(t *testing.T) {
	wk, cmds := pendingRTTY(t)

	wk.processByte(0x00)
	wk.processByte(0x03) // host close
	expectCmdTimeout(t, cmds, "RX", 300*time.Millisecond)
}

// TestResetStopsTune: an admin reset during a tune carrier must unkey the radio.
func TestResetStopsTune(t *testing.T) {
	wk, _, cmds := newTestWK(t)
	wk.processByte(0x0B)
	wk.processByte(0x01) // tune on
	expectCmd(t, cmds, "TX")

	wk.processByte(0x00)
	wk.processByte(0x01) // admin reset
	expectCmdTimeout(t, cmds, "RX", 300*time.Millisecond)
}

// TestHostCloseAbortsCWInProgress: CW already handed to the radio must be
// aborted when the logger closes the host.
func TestHostCloseAbortsCWInProgress(t *testing.T) {
	wk, _, cmds := newTestWK(t)
	wk.mu.Lock()
	wk.wpm = 5
	wk.mu.Unlock()

	wk.processByte('E')
	expectCmd(t, cmds, "KY E")

	wk.processByte(0x00)
	wk.processByte(0x03) // host close
	expectCmdTimeout(t, cmds, "RX", 300*time.Millisecond)
}
