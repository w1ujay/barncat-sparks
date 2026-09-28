package cat

import (
	"errors"
	"fmt"
	"net"
	"sync/atomic"
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
		{"RU", false}, {"RD", false},
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
	start := time.Now()
	_, err := c.Raw("XX")
	if !errors.Is(err, ErrRadioRejected) || errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("err = %v, want ErrRadioRejected and not ErrQueryTimeout", err)
	}
	// A rejection must not hold queryMu (and the KY sender behind it) for
	// the whole query timeout.
	if d := time.Since(start); d >= 250*time.Millisecond {
		t.Fatalf("rejected query took %v, want well under the %v timeout", d, rawQueryTimeout)
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

// lateReplyResponder answers the first FA only after rawQueryTimeout (a
// slow radio), later FAs at once, and ID with ID017. It answers in command
// order, as a radio on serial does.
func lateReplyResponder(t *testing.T) (net.Listener, chan string) {
	t.Helper()
	fas := 0
	return mockCATResponder(t, func(cmd string, conn net.Conn) {
		switch cmd {
		case "FA":
			fas++
			if fas == 1 {
				time.Sleep(rawQueryTimeout + 100*time.Millisecond)
			}
			fmt.Fprintf(conn, "FA%011d;", fas)
		case "ID":
			fmt.Fprint(conn, "ID017;")
		}
	})
}

// Regression: a query times out and its reply arrives late. With a sync
// query set, the next query must resync and return its own reply, not the
// late one.
func TestRawResyncsAfterTimeout(t *testing.T) {
	ln, cmds := lateReplyResponder(t)
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	if _, err := c.Raw("FA"); !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("first Raw(FA) err = %v, want ErrQueryTimeout", err)
	}
	got, err := c.Raw("FA")
	if err != nil || got != "FA00000000002" {
		t.Fatalf("second Raw(FA) = %q, %v; want its own reply FA00000000002, not the late FA00000000001", got, err)
	}
	for _, want := range []string{"FA", "ID", "FA"} {
		expectCmd(t, cmds, want)
	}
	// Back in sync: the next query sends no ID.
	if got, err := c.Raw("FA"); err != nil || got != "FA00000000003" {
		t.Fatalf("third Raw(FA) = %q, %v; want FA00000000003", got, err)
	}
	expectCmd(t, cmds, "FA")
}

// A radio that doesn't answer the resync fails the query with
// ErrQueryTimeout and never sends it.
func TestRawResyncFailureDoesNotSendQuery(t *testing.T) {
	ln, cmds := mockCATResponder(t, func(string, net.Conn) {})
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	if _, err := c.Raw("FA"); !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("err = %v, want ErrQueryTimeout", err)
	}
	if _, err := c.Raw("FB"); !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("err = %v, want ErrQueryTimeout", err)
	}
	for _, want := range []string{"FA", "ID"} {
		expectCmd(t, cmds, want)
	}
	select {
	case got := <-cmds:
		t.Fatalf("sent %q after a failed resync, want nothing", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// With no sync query set (the K4 path), rawQuery behaves as before: no ID
// is ever sent.
func TestRawNoSyncQuerySendsNoID(t *testing.T) {
	ln, cmds := lateReplyResponder(t)
	c := connectClient(t, ln)
	if _, err := c.Raw("FA"); !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("first Raw(FA) err = %v, want ErrQueryTimeout", err)
	}
	c.Raw("FA")
	for _, want := range []string{"FA", "FA"} {
		expectCmd(t, cmds, want)
	}
	select {
	case got := <-cmds:
		t.Fatalf("sent %q with no sync query set", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// Regression: a resync that timed out leaves its ID reply in flight; it must
// not end a later resync early. The responder answers in order: the first
// FA and the first two IDs late (after rawQueryTimeout), everything else at
// once (the first FB 50 ms late), FB numbered by how many FBs it has seen.
//
//	Q1 FA: times out (its reply is late).
//	Q2 FA: resync ID #1 times out.
//	Q3 FB: resync ID #2; ID #1's late reply must not end it.
//	Q4 FB: must return the reply to the FB it sent, not a late one.
func TestRawLostResyncReplyDoesNotEndLaterResync(t *testing.T) {
	late := rawQueryTimeout + 100*time.Millisecond
	var ids, fbs atomic.Int32
	fas := 0
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		switch cmd {
		case "FA":
			fas++
			if fas == 1 {
				time.Sleep(late)
			}
			fmt.Fprintf(conn, "FA%011d;", fas)
		case "FB":
			n := fbs.Add(1)
			if n == 1 {
				// A little after the ID before it, so a drain right after
				// that ID can't hide a late FB reply.
				time.Sleep(50 * time.Millisecond)
			}
			fmt.Fprintf(conn, "FB%011d;", n)
		case "ID":
			if ids.Add(1) <= 2 {
				time.Sleep(late)
			}
			fmt.Fprint(conn, "ID017;")
		}
	})
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	for i, cmd := range []string{"FA", "FA", "FB"} {
		if _, err := c.Raw(cmd); !errors.Is(err, ErrQueryTimeout) {
			t.Fatalf("Q%d Raw(%s) err = %v, want ErrQueryTimeout", i+1, cmd, err)
		}
	}
	got, err := c.Raw("FB")
	if err != nil {
		t.Fatalf("Q4 Raw(FB): %v", err)
	}
	// Fence: the responder answers in order, so once FA is answered it has
	// counted every FB sent before it.
	if _, err := c.Raw("FA"); err != nil {
		t.Fatalf("fence Raw(FA): %v", err)
	}
	if want := fmt.Sprintf("FB%011d", fbs.Load()); got != want {
		t.Fatalf("Q4 Raw(FB) = %q, want the reply to its own FB %q", got, want)
	}
}

// A lost-resync record past syncStaleWindow (the radio was off, so its reply
// never came) must not make a resync skip its own reply.
func TestRawExpiredLostResyncIgnored(t *testing.T) {
	ln, cmds := mockCATResponder(t, func(cmd string, conn net.Conn) {
		switch cmd {
		case "FA":
			fmt.Fprint(conn, "FA00014083999;")
		case "ID":
			fmt.Fprint(conn, "ID017;")
		}
	})
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	c.queryMu.Lock()
	c.desync = true
	c.lostSyncs = []time.Time{time.Now().Add(-2 * syncStaleWindow)}
	c.queryMu.Unlock()
	if got, err := c.Raw("FA"); err != nil || got != "FA00014083999" {
		t.Fatalf("Raw(FA) = %q, %v; want FA00014083999", got, err)
	}
	for _, want := range []string{"ID", "FA"} {
		expectCmd(t, cmds, want)
	}
}

// A query whose send fails may still have partly reached the radio, so the
// next query resyncs.
func TestRawSendFailureMarksDesync(t *testing.T) {
	c := NewCATClient() // not connected: Send fails
	c.setSyncQuery("ID")
	if _, err := c.Raw("FA"); err == nil {
		t.Fatal("Raw(FA) on an unconnected client succeeded")
	}
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	if !c.desync {
		t.Fatal("desync not set after a failed send")
	}
}
