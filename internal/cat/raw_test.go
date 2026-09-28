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
//	Q3 FB: ID #1's late reply must not end a resync that then lets a late
//	       reply through (it did when Q3 sent ID #2 and took ID #1's reply).
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
	for i, cmd := range []string{"FA", "FA"} {
		if _, err := c.Raw(cmd); !errors.Is(err, ErrQueryTimeout) {
			t.Fatalf("Q%d Raw(%s) err = %v, want ErrQueryTimeout", i+1, cmd, err)
		}
	}
	// ownFB checks that a successful FB query got the reply to the last FB
	// sent. Fence: the responder answers in order, so once FA is answered it
	// has counted every FB sent before it.
	ownFB := func(q int, got string) {
		t.Helper()
		if _, err := c.Raw("FA"); err != nil {
			t.Fatalf("fence Raw(FA): %v", err)
		}
		if want := fmt.Sprintf("FB%011d", fbs.Load()); got != want {
			t.Fatalf("Q%d Raw(FB) = %q, want the reply to its own FB %q", q, got, want)
		}
	}
	// Q3 may time out (a fresh ID sent behind the lost one) or succeed once
	// the outstanding ID is answered; either way it must not take a late
	// reply.
	if got, err := c.Raw("FB"); err == nil {
		ownFB(3, got)
	} else if !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("Q3 Raw(FB) err = %v, want nil or ErrQueryTimeout", err)
	}
	got, err := c.Raw("FB")
	if err != nil {
		t.Fatalf("Q4 Raw(FB): %v", err)
	}
	ownFB(4, got)
}

// An outstanding sync older than syncStaleWindow (the radio was off, so its
// reply never came) is replaced: the next resync sends a fresh ID at once
// and ends on its reply.
func TestRawExpiredSyncSendsFreshID(t *testing.T) {
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
	c.syncSent = time.Now().Add(-2 * syncStaleWindow)
	c.queryMu.Unlock()
	start := time.Now()
	if got, err := c.Raw("FA"); err != nil || got != "FA00014083999" {
		t.Fatalf("Raw(FA) = %q, %v; want FA00014083999", got, err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("Raw(FA) took %v; an expired sync must be replaced at once", d)
	}
	for _, want := range []string{"ID", "FA"} {
		expectCmd(t, cmds, want)
	}
}

// While a sync is outstanding (younger than syncStaleWindow), another
// resync attempt sends no second ID: at most one is in flight, so the ID
// reply that ends a resync is always the outstanding one's.
func TestRawOutstandingSyncSendsNoSecondID(t *testing.T) {
	old := syncStaleWindow
	syncStaleWindow = 3 * time.Second // outlasts the three queries below
	t.Cleanup(func() { syncStaleWindow = old })
	var ids atomic.Int32
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "ID" {
			ids.Add(1) // never answered: the radio is off
		}
	})
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	for i, cmd := range []string{"FA", "FB", "FB"} {
		if _, err := c.Raw(cmd); !errors.Is(err, ErrQueryTimeout) {
			t.Fatalf("Q%d Raw(%s) err = %v, want ErrQueryTimeout", i+1, cmd, err)
		}
	}
	time.Sleep(50 * time.Millisecond) // let the responder see what was sent
	if n := ids.Load(); n != 1 {
		t.Fatalf("sent %d ID queries, want 1 (one outstanding sync)", n)
	}
}

// Power-on: the radio is silent for 3 s under continuous queries, then
// answers. The first query succeeds within about syncStaleWindow (the last
// sync sent while off expires and a fresh one goes out), not after a pile
// of syncs sent while it was off have all expired.
func TestRawRecoversPromptlyAfterPowerOn(t *testing.T) {
	var on atomic.Bool
	ln, cmds := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if !on.Load() {
			return
		}
		switch cmd {
		case "FA":
			fmt.Fprint(conn, "FA00014083999;")
		case "ID":
			fmt.Fprint(conn, "ID017;")
		}
	})
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() { // the responder blocks once its command log fills
		for {
			select {
			case <-cmds:
			case <-done:
				return
			}
		}
	}()
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	for off := time.Now(); time.Since(off) < 3*time.Second; {
		c.Raw("FA")
	}
	on.Store(true)
	powerOn := time.Now()
	for {
		if _, err := c.Raw("FA"); err == nil {
			break
		}
		if time.Since(powerOn) > 10*time.Second {
			t.Fatal("no query succeeded within 10 s of power-on")
		}
	}
	if d := time.Since(powerOn); d >= 1500*time.Millisecond {
		t.Fatalf("first query succeeded %v after power-on, want under 1.5 s (1 s window)", d)
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
	desync := c.desync
	c.queryMu.Unlock()
	if !desync {
		t.Fatal("desync not set after a failed send")
	}
	// The resync's own send fails too; it is still recorded as outstanding,
	// since a partial write may draw a reply.
	if _, err := c.Raw("FA"); err == nil {
		t.Fatal("second Raw(FA) on an unconnected client succeeded")
	}
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	if c.syncSent.IsZero() {
		t.Fatal("failed sync send not recorded as outstanding")
	}
}

// Regression: an ordinary ID query abandoned after a stray "?;" leaves its
// ID017 in flight. It must count as the outstanding sync: a resync that sent
// a second ID and took the first one's reply left its own ID017 to end a
// later resync early, and a later FA got an earlier FA's reply. Replies are
// in order: "?" for ZZ9 after 20 ms, the first ID after 100 ms, any second
// ID after 550 ms, the first FA 50 ms after its read; FA numbered by count.
func TestRawAbandonedIDQueryIsOutstandingSync(t *testing.T) {
	var ids, fas atomic.Int32
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		switch cmd {
		case "ZZ9":
			time.Sleep(20 * time.Millisecond)
			fmt.Fprint(conn, "?;")
		case "ID":
			switch ids.Add(1) {
			case 1:
				time.Sleep(100 * time.Millisecond)
			case 2:
				time.Sleep(550 * time.Millisecond)
			}
			fmt.Fprint(conn, "ID017;")
		case "FA":
			n := fas.Add(1)
			if n == 1 {
				time.Sleep(50 * time.Millisecond)
			}
			fmt.Fprintf(conn, "FA%011d;", n)
		case "FB":
			fmt.Fprint(conn, "FB00000000001;")
		}
	})
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	if _, err := c.Raw("ZZ9"); err != nil {
		t.Fatalf("Raw(ZZ9): %v", err)
	}
	if _, err := c.Raw("ID"); !errors.Is(err, ErrRadioRejected) {
		t.Fatalf("Raw(ID) err = %v, want ErrRadioRejected (the stray ?)", err)
	}
	// ownFA checks that a successful FA query got the reply to the last FA
	// sent. Fence: once FB is answered, every FA before it was counted.
	ownFA := func(q int, got string) {
		t.Helper()
		if _, err := c.Raw("FB"); err != nil {
			t.Fatalf("fence Raw(FB): %v", err)
		}
		if want := fmt.Sprintf("FA%011d", fas.Load()); got != want {
			t.Fatalf("FA query %d = %q, want the reply to its own FA %q", q, got, want)
		}
	}
	if got, err := c.Raw("FA"); err == nil {
		ownFA(1, got)
	} else if !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("first Raw(FA) err = %v, want nil or ErrQueryTimeout", err)
	}
	got, err := c.Raw("FA")
	if err != nil {
		t.Fatalf("second Raw(FA): %v", err)
	}
	ownFA(2, got)
}

// An ordinary ID query that times out is the outstanding sync: neither a
// later resync nor a later ID query sends a second ID while it is
// unexpired; each waits for the outstanding one's reply.
func TestRawAbandonedIDQuerySendsNoSecondID(t *testing.T) {
	old := syncStaleWindow
	syncStaleWindow = 3 * time.Second // outlasts the queries below
	t.Cleanup(func() { syncStaleWindow = old })
	var ids atomic.Int32
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "ID" {
			ids.Add(1) // never answered: the radio is off
		}
	})
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	for i, cmd := range []string{"ID", "FB", "ID"} {
		if _, err := c.Raw(cmd); !errors.Is(err, ErrQueryTimeout) {
			t.Fatalf("Q%d Raw(%s) err = %v, want ErrQueryTimeout", i+1, cmd, err)
		}
	}
	time.Sleep(50 * time.Millisecond) // let the responder see what was sent
	if n := ids.Load(); n != 1 {
		t.Fatalf("sent %d ID queries, want 1 (one outstanding)", n)
	}
}

// Regression: an abandoned ID query's reply arrives while the client is idle
// for longer than syncStaleWindow and sits unread. The next resync must take
// it as the outstanding sync's answer, not declare the sync lost, send a
// replacement ID and take the old reply for the replacement's (leaving the
// replacement's reply to end a later resync early). Replies are in order:
// "?" for ZZ9 after 20 ms, the first ID after 100 ms, any second ID after
// 550 ms, the first FA 50 ms after its read; FA numbered by count.
func TestRawExpiredSyncTakesQueuedReply(t *testing.T) {
	var ids, fas atomic.Int32
	ln, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		switch cmd {
		case "ZZ9":
			time.Sleep(20 * time.Millisecond)
			fmt.Fprint(conn, "?;")
		case "ID":
			switch ids.Add(1) {
			case 1:
				time.Sleep(100 * time.Millisecond)
			case 2:
				time.Sleep(550 * time.Millisecond)
			}
			fmt.Fprint(conn, "ID017;")
		case "FA":
			n := fas.Add(1)
			if n == 1 {
				time.Sleep(50 * time.Millisecond)
			}
			fmt.Fprintf(conn, "FA%011d;", n)
		case "FB":
			fmt.Fprint(conn, "FB00000000001;")
		}
	})
	c := connectClient(t, ln)
	c.setSyncQuery("ID")
	if _, err := c.Raw("ZZ9"); err != nil {
		t.Fatalf("Raw(ZZ9): %v", err)
	}
	if _, err := c.Raw("ID"); !errors.Is(err, ErrRadioRejected) {
		t.Fatalf("Raw(ID) err = %v, want ErrRadioRejected (the stray ?)", err)
	}
	time.Sleep(syncStaleWindow + 100*time.Millisecond) // idle: ID017 arrives unread
	ownFA := func(q int, got string) {
		t.Helper()
		if _, err := c.Raw("FB"); err != nil {
			t.Fatalf("fence Raw(FB): %v", err)
		}
		if want := fmt.Sprintf("FA%011d", fas.Load()); got != want {
			t.Fatalf("FA query %d = %q, want the reply to its own FA %q", q, got, want)
		}
	}
	if got, err := c.Raw("FA"); err == nil {
		ownFA(1, got)
	} else if !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("first Raw(FA) err = %v, want nil or ErrQueryTimeout", err)
	}
	got, err := c.Raw("FA")
	if err != nil {
		t.Fatalf("second Raw(FA): %v", err)
	}
	ownFA(2, got)
	if n := ids.Load(); n != 1 {
		t.Errorf("sent %d ID queries, want 1 (the queued reply answered the first)", n)
	}
}

// The mid-wait sync replacement extends a query's wait by at most one
// rawQueryTimeout only because the window is longer than the timeout.
func TestSyncStaleWindowExceedsQueryTimeout(t *testing.T) {
	if syncStaleWindow <= rawQueryTimeout {
		t.Fatalf("syncStaleWindow %v must exceed rawQueryTimeout %v", syncStaleWindow, rawQueryTimeout)
	}
}
