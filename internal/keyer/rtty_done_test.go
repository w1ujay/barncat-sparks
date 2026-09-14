package keyer

import (
	"testing"
	"time"
)

// completedRTTY sends one RTTY message and waits for its delayed RX, leaving a
// keyer with nothing on the air.
func completedRTTY(t *testing.T) (*Winkeyer, chan string) {
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
	expectCmdTimeout(t, cmds, "RX", 5*time.Second)
	time.Sleep(50 * time.Millisecond) // let the ] worker finish its bookkeeping
	return wk, cmds
}

func expectNoCmd(t *testing.T, cmds chan string, d time.Duration) {
	t.Helper()
	select {
	case cmd := <-cmds:
		t.Fatalf("unexpected CAT command %q", cmd)
	case <-time.After(d):
	}
}

// TestCloseAfterCompletedRTTYSendsNothing: once an RTTY message has finished
// (its RX already sent), Stop has nothing to release and must not unkey a
// transmission started some other way.
func TestCloseAfterCompletedRTTYSendsNothing(t *testing.T) {
	wk, cmds := completedRTTY(t)
	wk.Close()
	expectNoCmd(t, cmds, 200*time.Millisecond)
}

// TestClearBufferAfterCompletedRTTYSendsNothing: N1MM's clear buffer at the
// start of the next message must not send RX after a finished RTTY message.
func TestClearBufferAfterCompletedRTTYSendsNothing(t *testing.T) {
	wk, cmds := completedRTTY(t)
	wk.processByte(0x0A)
	expectNoCmd(t, cmds, 200*time.Millisecond)
}
