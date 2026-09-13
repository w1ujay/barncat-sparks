package cat

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe to write from a relay goroutine while a
// test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A CATServer client that stops reading must not stall the radio's responses
// for queries, decoding, or other clients.

func faResponder(t *testing.T) net.Listener {
	t.Helper()
	ln, cmds := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "FA" {
			fmt.Fprint(conn, "FA00014053284;")
		}
	})
	go func() {
		for range cmds {
		}
	}()
	return ln
}

func TestStuckRelayDoesNotBlockQueries(t *testing.T) {
	c := connectClient(t, faResponder(t))

	stuck, peer := net.Pipe() // nothing ever reads peer
	defer peer.Close()
	c.AddRelay(stuck)

	for i := 0; i < 3; i++ {
		hz, err := c.GetFreqA()
		if err != nil || hz != 14053284 {
			t.Fatalf("query %d: hz=%d err=%v; a stuck relay must not block replies", i, hz, err)
		}
	}
}

func TestStuckRelayIsDisconnected(t *testing.T) {
	c := connectClient(t, faResponder(t))

	stuck, peer := net.Pipe()
	defer peer.Close()
	c.AddRelay(stuck)
	if err := c.Send("FA;"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(3 * time.Second) // longer than the relay write timeout

	peer.SetReadDeadline(time.Now().Add(time.Second))
	_, err := peer.Read(make([]byte, 64))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read from the stuck client: err=%v; want EOF (it should have been disconnected)", err)
	}
}
