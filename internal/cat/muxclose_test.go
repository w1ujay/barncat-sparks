package cat

import (
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestRadioServerCloseDropsSilentMuxClient: with the HTTP mux on (amp proxy or
// wattmeter), a client that connects and sends nothing is still waiting in the
// protocol peek when Stop runs. Close must disconnect it, not leave its socket
// and goroutine behind.
func TestRadioServerCloseDropsSilentMuxClient(t *testing.T) {
	srv, err := NewRadioServer(&mockRadio{}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Handle("/api/test", http.NotFoundHandler())
	go srv.Run()

	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond) // let Run accept it and start peeking

	srv.Close()

	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(make([]byte, 1))
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("silent client left connected after Close (read err=%v)", err)
	}
}
