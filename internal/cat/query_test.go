package cat

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// TestConcurrentQueriesSurviveUnrelatedResponse: while MIDI tuning and a
// RadioServer client both wait on a query, an unrelated response (CATServer
// relay traffic shares the same response stream) arrives first. Each query
// must still get its own reply instead of the other one's.
func TestConcurrentQueriesSurviveUnrelatedResponse(t *testing.T) {
	ln, cmds := mockCATResponder(t, func(cmd string, conn net.Conn) {
		switch cmd {
		case "FA":
			// Hold the reply until the FB query is waiting too, then send an
			// unrelated response ahead of it.
			time.Sleep(20 * time.Millisecond)
			fmt.Fprint(conn, "IF00014053284;FA00014053284;")
		case "FB":
			fmt.Fprint(conn, "FB00007025000;")
		}
	})
	go func() {
		for range cmds {
		}
	}()
	c := connectClient(t, ln)

	var wg sync.WaitGroup
	var a, b int
	var errA, errB error
	wg.Add(2)
	go func() { defer wg.Done(); a, errA = c.GetFreqA() }()
	time.Sleep(5 * time.Millisecond) // FA query is waiting before FB is sent
	go func() { defer wg.Done(); b, errB = c.GetFreqB() }()
	wg.Wait()

	if errA != nil || errB != nil {
		t.Fatalf("GetFreqA err=%v, GetFreqB err=%v", errA, errB)
	}
	if a != 14053284 || b != 7025000 {
		t.Fatalf("got FA=%d FB=%d, want 14053284 and 7025000", a, b)
	}
}
