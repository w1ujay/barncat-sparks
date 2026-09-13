package cat

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// A connection accepted just before Close reaches handleClient after the
// client map is gone. It must be closed, not panic the whole process.

func TestRadioServerClientAfterCloseDoesNotPanic(t *testing.T) {
	srv, err := NewRadioServer(&mockRadio{}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	expectHandledAfterClose(t, srv.handleClient)
}

func TestCATServerClientAfterCloseDoesNotPanic(t *testing.T) {
	srv, err := NewCATServer(NewCATClient(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	expectHandledAfterClose(t, srv.handleClient)
}

// expectHandledAfterClose runs handle synchronously on one end of a pipe and
// checks it neither panics nor leaves the connection open.
func expectHandledAfterClose(t *testing.T, handle func(net.Conn)) {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("handleClient panicked after Close: %v", r)
			}
		}()
		handle(server)
	}()

	client.SetReadDeadline(time.Now().Add(time.Second))
	_, err := client.Read(make([]byte, 1))
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("connection left open after Close (read err=%v)", err)
	}
}
