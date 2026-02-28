package keyer

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
	"go.bug.st/serial"
)

// testPort satisfies serial.Port using a buffered channel for writes.
// This avoids the synchronous blocking of io.Pipe that would deadlock
// when processByte writes a status byte before any reader is ready.
type testPort struct {
	written chan byte
}

func newTestPort() *testPort {
	return &testPort{written: make(chan byte, 256)}
}

func (p *testPort) Read([]byte) (int, error)                             { select {} }
func (p *testPort) Write(b []byte) (int, error) {
	for _, v := range b {
		p.written <- v
	}
	return len(b), nil
}
func (p *testPort) Close() error                                         { return nil }
func (p *testPort) ResetInputBuffer() error                              { return nil }
func (p *testPort) ResetOutputBuffer() error                             { return nil }
func (p *testPort) SetDTR(bool) error                                    { return nil }
func (p *testPort) SetRTS(bool) error                                    { return nil }
func (p *testPort) SetReadTimeout(time.Duration) error                   { return nil }
func (p *testPort) GetModemStatusBits() (*serial.ModemStatusBits, error) { return nil, nil }
func (p *testPort) SetMode(*serial.Mode) error                           { return nil }
func (p *testPort) Break(time.Duration) error                            { return nil }
func (p *testPort) Drain() error                                         { return nil }

// readByte reads one byte from the test port with a timeout.
func readByte(t *testing.T, p *testPort, timeout time.Duration) byte {
	t.Helper()
	select {
	case b := <-p.written:
		return b
	case <-time.After(timeout):
		t.Fatalf("timed out reading byte from test port")
		return 0
	}
}

// expectStatus reads a status byte and checks the busy flag.
func expectStatus(t *testing.T, p *testPort, wantBusy bool) {
	t.Helper()
	b := readByte(t, p, 2*time.Second)
	if b&0xC0 != 0xC0 {
		t.Fatalf("expected status byte (0xC0 prefix), got 0x%02x", b)
	}
	gotBusy := b&StatusBusy != 0
	if gotBusy != wantBusy {
		t.Fatalf("status busy=%v, want busy=%v (byte=0x%02x)", gotBusy, wantBusy, b)
	}
}

// waitIdle drains bytes from the test port until a non-busy status byte arrives.
func waitIdle(t *testing.T, p *testPort) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case b := <-p.written:
			if b&0xC0 == 0xC0 && b&StatusBusy == 0 {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for idle status")
		}
	}
}

// mockCAT starts a TCP server that captures received commands.
// Returns the listener and a channel that receives each semicolon-delimited command.
func mockCAT(t *testing.T) (net.Listener, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cmds := make(chan string, 32)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		scanner.Split(cat.SplitSemicolon)
		for scanner.Scan() {
			txt := scanner.Text()
			if txt != "" {
				cmds <- txt
			}
		}
	}()
	return ln, cmds
}

// connectClient creates a CATClient connected to the given listener and
// registers cleanup that closes the client then the listener, with a small
// delay so the readLoop goroutine can exit before conn is nilled.
func connectClient(t *testing.T, ln net.Listener) *cat.CATClient {
	t.Helper()
	c := cat.NewCATClient()
	if err := c.ConnectTCP(ln.Addr().String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
		// Give readLoop time to see the closed connection and exit before
		// the test runner tears down the listener and goroutines.
		time.Sleep(50 * time.Millisecond)
		ln.Close()
	})
	return c
}

func expectCmd(t *testing.T, cmds chan string, want string) {
	t.Helper()
	expectCmdTimeout(t, cmds, want, 2*time.Second)
}

func expectCmdTimeout(t *testing.T, cmds chan string, want string, timeout time.Duration) {
	t.Helper()
	select {
	case got := <-cmds:
		if got != want {
			t.Errorf("got command %q, want %q", got, want)
		}
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for command %q", want)
	}
}

// newTestWK creates a Winkeyer with a mock CAT client and a buffered test port.
func newTestWK(t *testing.T) (*Winkeyer, *testPort, chan string) {
	t.Helper()
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)

	wk := NewWinkeyer(c)
	wk.hostOpen = true

	tp := newTestPort()
	wk.port = tp

	return wk, tp, cmds
}

// --- RTTY Protocol Tests ---

func TestRTTYAdminEnable(t *testing.T) {
	wk, _, cmds := newTestWK(t)

	// Admin 0x13: FSK mode enable
	// P1: 0x80 = enable, P2: ignored
	wk.processByte(0x00) // admin prefix
	wk.processByte(0x13) // FSK subcode
	wk.processByte(0x80) // P1: enable bit set
	wk.processByte(0x00) // P2

	wk.mu.Lock()
	enabled := wk.rttyEnabled
	wk.mu.Unlock()

	if !enabled {
		t.Fatal("rttyEnabled should be true after admin 0x13 with bit 7 set")
	}

	// Admin 0x13 sets internal state only — no MD6/DT2 sent (N1MM manages radio mode)
	_ = cmds
}

func TestRTTYAdminDisable(t *testing.T) {
	wk, _, cmds := newTestWK(t)

	// Enable first
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)

	// Disable: P1 bit 7 clear
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x00)
	wk.processByte(0x00)

	wk.mu.Lock()
	enabled := wk.rttyEnabled
	wk.mu.Unlock()

	if enabled {
		t.Fatal("rttyEnabled should be false after admin 0x13 with bit 7 clear")
	}

	// No MD3 sent — N1MM manages radio mode, not the WinKeyer emulator
	_ = cmds
}

func TestRTTYAdminConfigBits(t *testing.T) {
	wk, _, cmds := newTestWK(t)

	// P1: enable + diddle + autoCRLF + baud=2 (75 baud)
	// bit7=1, bit6=1, bit4=1, bits1:0=10
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0xD2) // 1101_0010
	wk.processByte(0x00)

	_ = cmds // no CAT commands sent — admin 0x13 is internal state only

	wk.mu.Lock()
	diddle := wk.rttyDiddle
	autoCR := wk.rttyAutoCRLF
	baud := wk.rttyBaudRate
	wk.mu.Unlock()

	if !diddle {
		t.Error("rttyDiddle should be true")
	}
	if !autoCR {
		t.Error("rttyAutoCRLF should be true")
	}
	if baud != 2 {
		t.Errorf("rttyBaudRate = %d, want 2", baud)
	}
}

func TestRTTYBracketFlow(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	// Enable RTTY
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)

	// Drain echo bytes from enable
	drainPort(tp, 100*time.Millisecond)

	// Send [TEST]
	wk.processByte('[')
	expectCmd(t, cmds, "TX") // PTT on

	wk.processByte('T')
	wk.processByte('E')
	wk.processByte('S')
	wk.processByte('T')

	wk.processByte(']')

	// Should flush "TEST" via KY, then RX after txDelay (no EOT — we control PTT)
	expectCmd(t, cmds, "KY TEST")
	expectCmdTimeout(t, cmds, "RX", 5*time.Second) // PTT off after txDelay
}

func TestRTTYAbort(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	// Enable RTTY
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	// Start bracket, type some text, then abort
	wk.processByte('[')
	expectCmd(t, cmds, "TX")

	wk.processByte('A')
	wk.processByte('B')

	wk.processByte('\\') // abort

	// Should drop PTT without sending text
	expectCmd(t, cmds, "RX")

	// Verify buffer is clear
	wk.mu.Lock()
	inBracket := wk.rttyInBracket
	bufLen := len(wk.rttyBuf)
	wk.mu.Unlock()

	if inBracket {
		t.Error("rttyInBracket should be false after abort")
	}
	if bufLen != 0 {
		t.Errorf("rttyBuf should be empty after abort, got %d bytes", bufLen)
	}
}

func TestRTTYSpecialChars(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	// Enable RTTY
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	// Test } (CR/LF) inside bracket
	wk.processByte('[')
	expectCmd(t, cmds, "TX")

	wk.processByte('H')
	wk.processByte('I')
	wk.processByte('}') // CR/LF

	wk.processByte(']')

	// Should contain "HI\r\n"
	expectCmd(t, cmds, "KY HI\r\n")
	expectCmdTimeout(t, cmds, "RX", 5*time.Second)
}

func TestRTTYTestPattern(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	// Enable RTTY
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	// Test { (RYRYRY) inside bracket
	wk.processByte('[')
	expectCmd(t, cmds, "TX")

	wk.processByte('{') // RYRYRY pattern — buffered, flushed on ]
	wk.processByte(']')

	// All text flushed as single KY on close bracket
	expectCmd(t, cmds, "KY RYRYRYRYRY")
	expectCmdTimeout(t, cmds, "RX", 5*time.Second)
}

func TestRTTYLongText(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	// Enable RTTY
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	wk.processByte('[')
	expectCmd(t, cmds, "TX")

	// Send 30 chars — all buffered until ]
	for i := range 30 {
		wk.processByte(byte('A' + (i % 26)))
	}

	wk.processByte(']')

	// All 30 chars flushed as single KY (no chunking — K4 drops back-to-back KYs)
	expectCmd(t, cmds, "KY ABCDEFGHIJKLMNOPQRSTUVWXYZABCD")
	// RX is delayed by rttyTxDelay (30 chars at 45.45 baud ≈ 6s + 1s padding)
	expectCmdTimeout(t, cmds, "RX", 10*time.Second)
}

func TestRTTYUppercase(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	// Enable RTTY
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	wk.processByte('[')
	expectCmd(t, cmds, "TX")

	// Lowercase should be uppercased
	wk.processByte('h')
	wk.processByte('i')

	wk.processByte(']')

	expectCmd(t, cmds, "KY HI")
	expectCmdTimeout(t, cmds, "RX", 5*time.Second)
}

func TestRTTYCWStillWorksOutsideBrackets(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	// Enable RTTY
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	// Set high WPM for fast echo
	wk.mu.Lock()
	wk.wpm = 99
	wk.mu.Unlock()

	// Characters outside brackets go to CW buffer
	wk.processByte('T')

	// Should see busy status, then KY (K4 break-in handles PTT for CW)
	expectStatus(t, tp, true)
	expectCmd(t, cmds, "KY T")
}

func TestRTTYResetClearsState(t *testing.T) {
	wk, tp, cmds := newTestWK(t)

	// Enable RTTY and start a bracket
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	wk.processByte('[')
	expectCmd(t, cmds, "TX")

	wk.processByte('A')

	// Admin reset
	wk.processByte(0x00)
	wk.processByte(0x01) // reset

	// Reset should send RX (PTT off) — no MD3, N1MM manages radio mode
	expectCmd(t, cmds, "RX")

	wk.mu.Lock()
	enabled := wk.rttyEnabled
	inBracket := wk.rttyInBracket
	wk.mu.Unlock()

	if enabled {
		t.Error("rttyEnabled should be false after reset")
	}
	if inBracket {
		t.Error("rttyInBracket should be false after reset")
	}
}

func TestRTTYEcho(t *testing.T) {
	wk, tp, _ := newTestWK(t)

	// Enable RTTY
	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)
	drainPort(tp, 100*time.Millisecond)

	// Verify serial echo is on (mode register bit 2)
	wk.mu.Lock()
	wk.modeReg = 0x04 // echo on
	wk.mu.Unlock()

	wk.processByte('[')
	b := readByte(t, tp, time.Second)
	if b != '[' {
		t.Errorf("expected echo of '[', got 0x%02x", b)
	}

	wk.processByte('A')
	b = readByte(t, tp, time.Second)
	if b != 'A' {
		t.Errorf("expected echo of 'A', got 0x%02x", b)
	}
}

func TestIsRTTY(t *testing.T) {
	wk, _, cmds := newTestWK(t)

	if wk.IsRTTY() {
		t.Error("IsRTTY() should be false initially")
	}

	wk.processByte(0x00)
	wk.processByte(0x13)
	wk.processByte(0x80)
	wk.processByte(0x00)

	if !wk.IsRTTY() {
		t.Error("IsRTTY() should be true after FSK enable")
	}
	_ = cmds
}

// drainPort reads and discards bytes from the test port until timeout.
func drainPort(p *testPort, timeout time.Duration) {
	for {
		select {
		case <-p.written:
		case <-time.After(timeout):
			return
		}
	}
}
