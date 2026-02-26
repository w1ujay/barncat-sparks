// catclient_test.go
package cat

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"
)

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
		scanner.Split(SplitSemicolon)
		for scanner.Scan() {
			txt := scanner.Text()
			if txt != "" {
				cmds <- txt
			}
		}
	}()
	return ln, cmds
}

// mockCATResponder starts a TCP server that reads commands and writes back
// responses using the supplied handler function. The handler receives each
// semicolon-delimited command and the connection, and may write a response.
// Returns the listener and a channel of received commands (for verification).
func mockCATResponder(t *testing.T, handler func(cmd string, conn net.Conn)) (net.Listener, chan string) {
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
		scanner.Split(SplitSemicolon)
		for scanner.Scan() {
			txt := scanner.Text()
			if txt != "" {
				cmds <- txt
				handler(txt, conn)
			}
		}
	}()
	return ln, cmds
}

// connectClient creates a CATClient connected to the given listener and
// registers cleanup that closes the client then the listener, with a small
// delay so the readLoop goroutine can exit before conn is nilled.
func connectClient(t *testing.T, ln net.Listener) *CATClient {
	t.Helper()
	c := NewCATClient()
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

func TestSetFreqA(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SetFreqA(14053284)
	expectCmd(t, cmds, "FA00014053284")
}

func TestSetFreqB(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SetFreqB(7025000)
	expectCmd(t, cmds, "FB00007025000")
}

func TestGetFreqA(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "FA" {
			fmt.Fprint(conn, "FA00014053284;")
		}
	})
	c := connectClient(t, ln)
	hz, err := c.GetFreqA()
	if err != nil {
		t.Fatalf("GetFreqA() error: %v", err)
	}
	if hz != 14053284 {
		t.Errorf("GetFreqA() = %d, want 14053284", hz)
	}
}

func TestSetVolume(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SetVolume(0, 30)
	expectCmd(t, cmds, "AG030")
}

func TestSetVolumeSub(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SetVolume(1, 45)
	expectCmd(t, cmds, "AG$045")
}

func TestSetVolumeClamped(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SetVolume(0, 999)
	expectCmd(t, cmds, "AG060")
}

func TestBandUp(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.BandUp()
	expectCmd(t, cmds, "BU")
}

func TestBandDown(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.BandDown()
	expectCmd(t, cmds, "BD")
}

func TestSelectVFO(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SelectVFO(0)
	expectCmd(t, cmds, "FT0")
	c.SelectVFO(1)
	expectCmd(t, cmds, "FT1")
}

func TestSwapVFO(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SwapVFO()
	expectCmd(t, cmds, "SWT11")
}

func TestSetDataMode(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SetDataMode()
	expectCmd(t, cmds, "MD6")
	expectCmd(t, cmds, "DT2")
}

func TestSetCWMode(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SetCWMode()
	expectCmd(t, cmds, "MD3")
}

func TestSendRTTYText(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SendRTTYText("TEST DE W1AW")
	expectCmd(t, cmds, "KY TEST DE W1AW")
}

func TestSendRTTYTextChunking(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	// 30 chars should split into 24 + 6
	c.SendRTTYText("ABCDEFGHIJKLMNOPQRSTUVWXABCDEF")
	expectCmd(t, cmds, "KY ABCDEFGHIJKLMNOPQRSTUVWX")
	expectCmd(t, cmds, "KY ABCDEF")
}

func TestSendRTTYEOT(t *testing.T) {
	ln, cmds := mockCAT(t)
	c := connectClient(t, ln)
	c.SendRTTYEOT()
	expectCmd(t, cmds, "KY |")
}

func TestQueryDecode(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "TT" {
			fmt.Fprint(conn, "TT05HELLO;")
		}
	})
	c := connectClient(t, ln)
	text, err := c.QueryDecode()
	if err != nil {
		t.Fatalf("QueryDecode() error: %v", err)
	}
	if text != "HELLO" {
		t.Errorf("QueryDecode() = %q, want %q", text, "HELLO")
	}
}

func TestQueryDecodeEmpty(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "TT" {
			fmt.Fprint(conn, "TT00;")
		}
	})
	c := connectClient(t, ln)
	text, err := c.QueryDecode()
	if err != nil {
		t.Fatalf("QueryDecode() error: %v", err)
	}
	if text != "" {
		t.Errorf("QueryDecode() = %q, want empty", text)
	}
}

func TestQueryDecodeTimeout(t *testing.T) {
	ln, _ := mockCAT(t)
	c := connectClient(t, ln)
	text, err := c.QueryDecode()
	if err != nil {
		t.Fatalf("QueryDecode() error: %v", err)
	}
	if text != "" {
		t.Errorf("QueryDecode() = %q, want empty on timeout", text)
	}
}

func TestTTResponseRoutedToDecodeCh(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "TT" {
			fmt.Fprint(conn, "TT03CQ ;")
		}
	})
	c := connectClient(t, ln)

	// Send TT query to trigger response
	c.Send("TT;")

	// TT response should arrive on decodeCh
	select {
	case resp := <-c.decodeCh:
		if resp != "TT03CQ " {
			t.Errorf("decodeCh got %q, want %q", resp, "TT03CQ ")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for decodeCh")
	}

	// respCh should be empty (TT should NOT go there)
	select {
	case resp := <-c.respCh:
		t.Errorf("respCh unexpectedly got %q", resp)
	case <-time.After(100 * time.Millisecond):
		// Good — nothing on respCh
	}
}

func TestRelay(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "FA" {
			fmt.Fprint(conn, "FA00014053284;")
		}
	})
	c := connectClient(t, ln)

	// Set up two relay buffers (simulating two TCP clients)
	var buf1, buf2 bytes.Buffer
	c.AddRelay(&buf1)
	c.AddRelay(&buf2)

	// Query should still work AND both relays should get a copy
	hz, err := c.GetFreqA()
	if err != nil {
		t.Fatalf("GetFreqA() error: %v", err)
	}
	if hz != 14053284 {
		t.Errorf("GetFreqA() = %d, want 14053284", hz)
	}

	// Give relays time to receive
	time.Sleep(100 * time.Millisecond)

	if got := buf1.String(); got != "FA00014053284;" {
		t.Errorf("relay1 got %q, want %q", got, "FA00014053284;")
	}
	if got := buf2.String(); got != "FA00014053284;" {
		t.Errorf("relay2 got %q, want %q", got, "FA00014053284;")
	}

	// Remove relays
	c.RemoveRelay(&buf1)
	c.RemoveRelay(&buf2)
}
