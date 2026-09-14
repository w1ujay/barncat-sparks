package cat

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// setupMuxServer starts a RadioServer with one HTTP route so connections are
// routed through the HTTP/CAT peek.
func setupMuxServer(t *testing.T) *RadioServer {
	t.Helper()
	srv, err := NewRadioServer(&mockRadio{freqA: 14060000}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Handle("/api/test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	go srv.Run()
	t.Cleanup(srv.Close)
	return srv
}

// TestMuxFragmentedHTTPRequest: TCP may deliver the request line in pieces;
// a first read of "G" must not be routed to the CAT handler.
func TestMuxFragmentedHTTPRequest(t *testing.T) {
	srv := setupMuxServer(t)
	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprint(conn, "G")
	time.Sleep(50 * time.Millisecond)
	fmt.Fprint(conn, "ET /api/test HTTP/1.1\r\nHost: sparks\r\nConnection: close\r\n\r\n")

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no HTTP response to a fragmented request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
}

// TestMuxShortCATRoutesWithoutWaiting: a CAT command that cannot be an HTTP
// method is routed on its first read, not after a peek timeout.
func TestMuxShortCATRoutesWithoutWaiting(t *testing.T) {
	srv := setupMuxServer(t)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	start := time.Now()
	c.send(t, "FA;")
	if got := c.recv(t); got != "FA00014060000" {
		t.Fatalf("got %q, want FA00014060000", got)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("CAT reply took %v; short commands must not wait on the HTTP peek", elapsed)
	}
}

// TestMuxSilentCATClientNotDropped: a CAT client (N1MM) may connect and send
// nothing for a long time. Only bytes already received are subject to the
// peek timeout.
func TestMuxSilentCATClientNotDropped(t *testing.T) {
	srv := setupMuxServer(t)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	time.Sleep(700 * time.Millisecond) // longer than any peek timeout

	c.send(t, "FA;")
	if got := c.recv(t); got != "FA00014060000" {
		t.Fatalf("got %q, want FA00014060000", got)
	}
}
