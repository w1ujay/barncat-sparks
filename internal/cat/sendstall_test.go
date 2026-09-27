package cat

import (
	"net"
	"testing"
	"time"
)

// A radio link that stops reading must not hang Close behind a stuck Send.
func TestCloseDoesNotWaitForStalledSend(t *testing.T) {
	conn, peer := net.Pipe() // nothing ever reads peer, so writes block
	defer peer.Close()
	c := NewCATClient()
	c.transport = conn

	sent := make(chan error, 1)
	go func() { sent <- c.Send("FA00014074000;") }()
	time.Sleep(50 * time.Millisecond) // let Send block in Write

	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind a stalled Send")
	}
	select {
	case err := <-sent:
		if err == nil {
			t.Error("stalled Send returned nil after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Send still blocked after Close")
	}
}

// A stalled write gives up after catWriteTimeout instead of blocking forever.
func TestSendTimesOutOnStalledLink(t *testing.T) {
	old := catWriteTimeout
	catWriteTimeout = 100 * time.Millisecond
	defer func() { catWriteTimeout = old }()

	conn, peer := net.Pipe()
	defer peer.Close()
	c := NewCATClient()
	c.transport = conn
	defer c.Close()

	sent := make(chan error, 1)
	go func() { sent <- c.Send("RX;") }()
	select {
	case err := <-sent:
		if err == nil {
			t.Error("stalled Send returned nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send blocked past its write timeout")
	}
}
