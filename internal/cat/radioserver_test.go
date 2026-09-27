package cat

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// mockRadio records method calls and returns canned query responses.
type mockRadio struct {
	mu     sync.Mutex
	calls  []string
	freqA  int
	freqB  int
	speed  int
	mode   int
	decode string
	raw    map[string]string
	rawErr error
}

func (m *mockRadio) record(call string) {
	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()
}

func (m *mockRadio) getCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.calls))
	copy(result, m.calls)
	return result
}

func (m *mockRadio) reset() {
	m.mu.Lock()
	m.calls = nil
	m.mu.Unlock()
}

func (m *mockRadio) Close()                   {}
func (m *mockRadio) SendKY(text string) error { m.record(fmt.Sprintf("SendKY(%s)", text)); return nil }
func (m *mockRadio) SendAbort() error         { m.record("SendAbort()"); return nil }
func (m *mockRadio) SendCWMessage(text string) error {
	m.record(fmt.Sprintf("SendCWMessage(%s)", text))
	return nil
}
func (m *mockRadio) SendCWElement(dit bool) error {
	m.record(fmt.Sprintf("SendCWElement(%v)", dit))
	return nil
}
func (m *mockRadio) SetSpeed(wpm int) error   { m.record(fmt.Sprintf("SetSpeed(%d)", wpm)); return nil }
func (m *mockRadio) QuerySpeed() (int, error) { m.record("QuerySpeed()"); return m.speed, nil }
func (m *mockRadio) SetPTT(on bool) error     { m.record(fmt.Sprintf("SetPTT(%v)", on)); return nil }
func (m *mockRadio) SetFreqA(hz int) error    { m.record(fmt.Sprintf("SetFreqA(%d)", hz)); return nil }
func (m *mockRadio) SetFreqB(hz int) error    { m.record(fmt.Sprintf("SetFreqB(%d)", hz)); return nil }
func (m *mockRadio) GetFreqA() (int, error)   { m.record("GetFreqA()"); return m.freqA, nil }
func (m *mockRadio) GetFreqB() (int, error)   { m.record("GetFreqB()"); return m.freqB, nil }
func (m *mockRadio) SelectVFO(ab int) error   { m.record(fmt.Sprintf("SelectVFO(%d)", ab)); return nil }
func (m *mockRadio) SwapVFO() error           { m.record("SwapVFO()"); return nil }
func (m *mockRadio) VFOAtoB() error           { m.record("VFOAtoB()"); return nil }
func (m *mockRadio) VFOBtoA() error           { m.record("VFOBtoA()"); return nil }
func (m *mockRadio) BandUp() error            { m.record("BandUp()"); return nil }
func (m *mockRadio) BandDown() error          { m.record("BandDown()"); return nil }
func (m *mockRadio) SetVolume(recv, level int) error {
	m.record(fmt.Sprintf("SetVolume(%d,%d)", recv, level))
	return nil
}
func (m *mockRadio) SetRFGain(recv, level int) error {
	m.record(fmt.Sprintf("SetRFGain(%d,%d)", recv, level))
	return nil
}
func (m *mockRadio) ToggleRFGain(recv int) error {
	m.record(fmt.Sprintf("ToggleRFGain(%d)", recv))
	return nil
}
func (m *mockRadio) SetPower(tenths int) error {
	m.record(fmt.Sprintf("SetPower(%d)", tenths))
	return nil
}
func (m *mockRadio) SetRIT(on bool) error   { m.record(fmt.Sprintf("SetRIT(%v)", on)); return nil }
func (m *mockRadio) ClearRIT() error        { m.record("ClearRIT()"); return nil }
func (m *mockRadio) SetXIT(on bool) error   { m.record(fmt.Sprintf("SetXIT(%v)", on)); return nil }
func (m *mockRadio) SetNB(on bool) error    { m.record(fmt.Sprintf("SetNB(%v)", on)); return nil }
func (m *mockRadio) SetNR(on bool) error    { m.record(fmt.Sprintf("SetNR(%v)", on)); return nil }
func (m *mockRadio) SetSplit(on bool) error { m.record(fmt.Sprintf("SetSplit(%v)", on)); return nil }
func (m *mockRadio) SetFilterBW(hz int) error {
	m.record(fmt.Sprintf("SetFilterBW(%d)", hz))
	return nil
}
func (m *mockRadio) SetNotch(on bool) error { m.record(fmt.Sprintf("SetNotch(%v)", on)); return nil }
func (m *mockRadio) SetNotchFreq(hz int) error {
	m.record(fmt.Sprintf("SetNotchFreq(%d)", hz))
	return nil
}
func (m *mockRadio) SetDataMode() error      { m.record("SetDataMode()"); return nil }
func (m *mockRadio) SetCWMode() error        { m.record("SetCWMode()"); return nil }
func (m *mockRadio) QueryMode() (int, error) { m.record("QueryMode()"); return m.mode, nil }
func (m *mockRadio) SendRTTYText(text string) error {
	m.record(fmt.Sprintf("SendRTTYText(%s)", text))
	return nil
}
func (m *mockRadio) SendRTTYEOT() error { m.record("SendRTTYEOT()"); return nil }
func (m *mockRadio) SetTextDecode(on bool) error {
	m.record(fmt.Sprintf("SetTextDecode(%v)", on))
	return nil
}
func (m *mockRadio) QueryDecode() (string, error) { m.record("QueryDecode()"); return m.decode, nil }

func (m *mockRadio) Raw(cmd string) (string, error) {
	m.record(fmt.Sprintf("Raw(%s)", cmd))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rawErr != nil {
		return "", m.rawErr
	}
	return m.raw[cmd], nil
}

// Compile-time check: *mockRadio implements Radio.
var _ Radio = (*mockRadio)(nil)

// testClient wraps a TCP connection with a semicolon-splitting scanner.
type testClient struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

func newTestClient(t *testing.T, addr string) *testClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(conn)
	scanner.Split(SplitSemicolon)
	return &testClient{conn: conn, scanner: scanner}
}

func (c *testClient) send(t *testing.T, cmd string) {
	t.Helper()
	_, err := fmt.Fprint(c.conn, cmd)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
}

func (c *testClient) recv(t *testing.T) string {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(time.Second))
	if c.scanner.Scan() {
		return c.scanner.Text()
	}
	if err := c.scanner.Err(); err != nil {
		t.Fatalf("recv: %v", err)
	}
	t.Fatal("recv: no response")
	return ""
}

func (c *testClient) close() {
	c.conn.Close()
}

// setupRadioServer creates a RadioServer with the given mock on a random port,
// starts it, and registers cleanup.
func setupRadioServer(t *testing.T, mock *mockRadio) *RadioServer {
	t.Helper()
	srv, err := NewRadioServer(mock, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Run()
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestRadioServerQueries(t *testing.T) {
	mock := &mockRadio{
		freqA:  14060000,
		freqB:  7030000,
		speed:  25,
		mode:   3,
		decode: "CQ TEST",
	}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	tests := []struct {
		cmd  string
		want string
	}{
		{"FA;", "FA00014060000"},
		{"FB;", "FB00007030000"},
		{"KS;", "KS025"},
		{"MD;", "MD3"},
		{"TT;", "TT07CQ TEST"},
	}

	for _, tt := range tests {
		c.send(t, tt.cmd)
		got := c.recv(t)
		if got != tt.want {
			t.Errorf("query %s: got %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func TestRadioServerSetCommands(t *testing.T) {
	mock := &mockRadio{freqA: 14060000}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	tests := []struct {
		cmd      string
		wantCall string
	}{
		{"FA00014060000;", "SetFreqA(14060000)"},
		{"FB00007030000;", "SetFreqB(7030000)"},
		{"KS025;", "SetSpeed(25)"},
		{"KY TEST;", "SendKY(TEST)"},
		{"KY |;", "SendRTTYEOT()"},
		{"TX;", "SetPTT(true)"},
		{"RX;", "SendAbort()"},
		{"BU;", "BandUp()"},
		{"BD;", "BandDown()"},
		{"FT0;", "SelectVFO(0)"},
		{"FT1;", "SelectVFO(1)"},
		{"SWT11;", "SwapVFO()"},
		{"AG025;", "SetVolume(0,25)"},
		{"AG$030;", "SetVolume(1,30)"},
		{"RG-15;", "SetRFGain(0,15)"},
		{"RG$-20;", "SetRFGain(1,20)"},
		{"RG/;", "ToggleRFGain(0)"},
		{"RG$/;", "ToggleRFGain(1)"},
		{"PC050L;", "SetPower(50)"},
		{"PC050H;", "SetPower(500)"},
		{"RT1;", "SetRIT(true)"},
		{"RT0;", "SetRIT(false)"},
		{"RC;", "ClearRIT()"},
		{"XT1;", "SetXIT(true)"},
		{"XT0;", "SetXIT(false)"},
		{"NB1;", "SetNB(true)"},
		{"NB0;", "SetNB(false)"},
		{"NR1;", "SetNR(true)"},
		{"NR0;", "SetNR(false)"},
		{"BW0300;", "SetFilterBW(3000)"},
		{"NM1;", "SetNotch(true)"},
		{"NM0;", "SetNotch(false)"},
		{"NM20001;", "SetNotchFreq(2000)"},
		{"MD3;", "SetCWMode()"},
		{"MD6;", "SetDataMode()"},
		{"TD$205;", "SetTextDecode(true)"},
		{"TD$000;", "SetTextDecode(false)"},
		{"KZ.;", "SendCWElement(true)"},
		{"KZ-;", "SendCWElement(false)"},
	}

	for _, tt := range tests {
		mock.reset()
		c.send(t, tt.cmd)
		// Barrier: send a query and read the response to ensure the set command
		// has been dispatched (commands are processed sequentially per client).
		c.send(t, "FA;")
		c.recv(t)

		calls := mock.getCalls()
		if len(calls) < 2 || calls[0] != tt.wantCall {
			t.Errorf("%s: got calls %v, want first call %q", tt.cmd, calls, tt.wantCall)
		}
	}
}

func TestRadioServerDTNoOp(t *testing.T) {
	mock := &mockRadio{freqA: 14060000}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	mock.reset()
	c.send(t, "DT2;")
	c.send(t, "FA;")
	c.recv(t)

	calls := mock.getCalls()
	if len(calls) != 1 || calls[0] != "GetFreqA()" {
		t.Errorf("DT2 should be no-op, got calls: %v", calls)
	}
}

func TestRadioServerKYPipe(t *testing.T) {
	mock := &mockRadio{freqA: 14060000}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	mock.reset()
	c.send(t, "KY |;")
	c.send(t, "FA;")
	c.recv(t)

	calls := mock.getCalls()
	if len(calls) < 1 || calls[0] != "SendRTTYEOT()" {
		t.Errorf("KY | should call SendRTTYEOT, got calls: %v", calls)
	}
}

func TestRadioServerUnrecognized(t *testing.T) {
	mock := &mockRadio{freqA: 14060000}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	// Unrecognized command should not crash — verify by sending a query after.
	c.send(t, "ZZ;")
	c.send(t, "FA;")
	got := c.recv(t)
	if got != "FA00014060000" {
		t.Errorf("after unrecognized cmd: got %q, want FA00014060000", got)
	}
}

func TestRadioServerMultiClient(t *testing.T) {
	mock := &mockRadio{freqA: 14060000, speed: 25}
	srv := setupRadioServer(t, mock)

	c1 := newTestClient(t, srv.Addr())
	defer c1.close()
	c2 := newTestClient(t, srv.Addr())
	defer c2.close()

	// Client 1 queries FA, client 2 queries KS.
	// Each should only receive its own response (no broadcast).
	c1.send(t, "FA;")
	c2.send(t, "KS;")

	resp1 := c1.recv(t)
	if resp1 != "FA00014060000" {
		t.Errorf("c1 got %q, want FA00014060000", resp1)
	}

	resp2 := c2.recv(t)
	if resp2 != "KS025" {
		t.Errorf("c2 got %q, want KS025", resp2)
	}
}
