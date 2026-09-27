package cat

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestIsBareQuery(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"IF", true}, {"SB", true}, {"DV$", true}, {"AG$", true},
		{"K2", true}, {"K3", true}, {"K4", true}, {"KY", true},
		{"MD2", false}, {"AG030", false}, {"K41", false}, {"KY HI", false},
		{"TX", false}, {"RX", false}, {"RC", false}, {"UP", false}, {"DN", false},
		{"A", false}, {"ag", false}, {"AG$0", false}, {"", false},
	}
	for _, tt := range tests {
		if got := isBareQuery(tt.cmd); got != tt.want {
			t.Errorf("isBareQuery(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestRawQueryReturnsMatchingReply(t *testing.T) {
	ln, cmds := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "IF" {
			// An unrelated response arrives first and must be skipped.
			fmt.Fprint(conn, "FA00014000000;IF00014083999     -000000 0006000001 ;")
		}
	})
	c := connectClient(t, ln)

	got, err := c.Raw("IF")
	if err != nil {
		t.Fatal(err)
	}
	if want := "IF00014083999     -000000 0006000001 "; got != want {
		t.Fatalf("Raw(IF) = %q, want %q", got, want)
	}
	expectCmd(t, cmds, "IF")
}

func TestRawSubReceiverQuery(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "AG$" {
			fmt.Fprint(conn, "AG$015;")
		}
	})
	c := connectClient(t, ln)
	got, err := c.Raw("AG$")
	if err != nil || got != "AG$015" {
		t.Fatalf("Raw(AG$) = %q, %v; want AG$015", got, err)
	}
}

func TestRawSetDoesNotWait(t *testing.T) {
	ln, cmds := mockCATResponder(t, func(string, net.Conn) {})
	c := connectClient(t, ln)
	start := time.Now()
	got, err := c.Raw("MD2")
	if err != nil || got != "" {
		t.Fatalf("Raw(MD2) = %q, %v; want \"\", nil", got, err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("Raw(MD2) took %v; a set must not wait for a reply", d)
	}
	expectCmd(t, cmds, "MD2")
}

// A "?;" with no matching reply by the deadline is a rejection, not a
// timeout (the radio is alive).
func TestRawRejectedWithoutMatchingReply(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) { fmt.Fprint(conn, "?;") })
	c := connectClient(t, ln)
	_, err := c.Raw("XX")
	if !errors.Is(err, ErrRadioRejected) || errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("err = %v, want ErrRadioRejected and not ErrQueryTimeout", err)
	}
}

// A late "?;" for an earlier passed-through set arrives just before the
// query's own reply; the query must still succeed. (It used to fail with
// ErrRadioRejected, and the KY sender dropped queued CW on it.)
func TestRawSkipsStrayRejection(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "KY" {
			fmt.Fprint(conn, "?;KY0;")
		}
	})
	c := connectClient(t, ln)
	if got, err := c.Raw("KY"); err != nil || got != "KY0" {
		t.Fatalf("Raw(KY) = %q, %v; want KY0", got, err)
	}
}

func TestRawTimeout(t *testing.T) {
	ln, _ := mockCATResponder(t, func(string, net.Conn) {})
	c := connectClient(t, ln)
	if _, err := c.Raw("IF"); !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("err = %v, want ErrQueryTimeout", err)
	}
}

func TestDoneClosesWhenLinkDrops(t *testing.T) {
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "BYE" {
			conn.Close()
		}
	})
	c := connectClient(t, ln)
	select {
	case <-c.Done():
		t.Fatal("Done closed while the link is up")
	default:
	}
	c.Send("BYE;")
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed after the radio closed the link")
	}
}
