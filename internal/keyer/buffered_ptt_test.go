package keyer

import (
	"io"
	"testing"
	"time"
)

// TestCloseReleasesBufferedPTT: PTT keyed by buffered command 0x18 must be
// released by Stop like any other PTT the keyer holds.
func TestCloseReleasesBufferedPTT(t *testing.T) {
	wk, _, cmds := newTestWK(t)
	wk.processByte(0x18)
	wk.processByte(0x01)
	expectCmd(t, cmds, "TX")

	wk.Close()
	expectCmdTimeout(t, cmds, "RX", 500*time.Millisecond)
}

// TestClearBufferReleasesBufferedPTT: N1MM's Esc unkeys a buffered PTT.
func TestClearBufferReleasesBufferedPTT(t *testing.T) {
	wk, _, cmds := newTestWK(t)
	wk.processByte(0x18)
	wk.processByte(0x01)
	expectCmd(t, cmds, "TX")

	wk.processByte(0x0A) // clear buffer
	expectCmdTimeout(t, cmds, "RX", 500*time.Millisecond)
}

// TestBufferedPTTOffThenCloseSendsNothing: a PTT the logger already dropped
// isn't unkeyed again at Stop.
func TestBufferedPTTOffThenCloseSendsNothing(t *testing.T) {
	wk, _, cmds := newTestWK(t)
	wk.processByte(0x18)
	wk.processByte(0x01)
	expectCmd(t, cmds, "TX")
	wk.processByte(0x18)
	wk.processByte(0x00)
	expectCmd(t, cmds, "RX")

	wk.Close()
	select {
	case got := <-cmds:
		t.Errorf("Close sent %q; want nothing", got)
	case <-time.After(200 * time.Millisecond):
	}
}

// eofPort is a testPort whose Read fails once eof is closed, like a logger's
// virtual serial port going away.
type eofPort struct {
	*testPort
	eof chan struct{}
}

func (p *eofPort) Read([]byte) (int, error) {
	<-p.eof
	return 0, io.EOF
}

// TestReadFailureReleasesTune: if the logger's port fails mid-tune, the
// release can never arrive, so Run must unkey the radio itself.
func TestReadFailureReleasesTune(t *testing.T) {
	wk, tp, cmds := newTestWK(t)
	port := &eofPort{testPort: tp, eof: make(chan struct{})}
	wk.port = port

	wk.processByte(0x0B) // key immediate
	wk.processByte(0x01) // tune on
	expectCmd(t, cmds, "TX")

	ran := make(chan struct{})
	go func() { wk.Run(); close(ran) }()
	close(port.eof)
	expectCmdTimeout(t, cmds, "RX", 500*time.Millisecond)
	<-ran
}
