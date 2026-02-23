package main

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestCATServerRelaysCommands(t *testing.T) {
	// Set up a mock radio that responds to FA queries
	radioLn, radioCmds := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "FA" {
			fmt.Fprint(conn, "FA00014053284;")
		}
	})

	// Create CATClient connected to the mock radio
	cat := NewCATClient()
	if err := cat.ConnectTCP(radioLn.Addr().String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cat.Close()
		time.Sleep(50 * time.Millisecond)
		radioLn.Close()
	})

	// Start CATServer
	srv, err := NewCATServer(cat, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Run()
	t.Cleanup(func() { srv.Close() })

	// Connect a TCP client (simulating N1MM)
	time.Sleep(50 * time.Millisecond)
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial CATServer: %v", err)
	}
	defer conn.Close()

	// Send a command through the CATServer
	fmt.Fprint(conn, "FA;")

	// The mock radio should receive it
	select {
	case cmd := <-radioCmds:
		if cmd != "FA" {
			t.Errorf("radio got %q, want %q", cmd, "FA")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command at radio")
	}

	// The client should get the response back
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Split(splitSemicolon)
	if scanner.Scan() {
		resp := scanner.Text()
		if resp != "FA00014053284" {
			t.Errorf("client got %q, want %q", resp, "FA00014053284")
		}
	} else {
		t.Fatal("no response from CATServer")
	}
}

func TestCATServerInternalAndExternalCoexist(t *testing.T) {
	// Mock radio that responds to KS queries
	radioLn, _ := mockCATResponder(t, func(cmd string, conn net.Conn) {
		if cmd == "KS" {
			fmt.Fprint(conn, "KS025;")
		}
	})

	cat := NewCATClient()
	if err := cat.ConnectTCP(radioLn.Addr().String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cat.Close()
		time.Sleep(50 * time.Millisecond)
		radioLn.Close()
	})

	srv, err := NewCATServer(cat, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Run()
	t.Cleanup(func() { srv.Close() })

	// sparks internal: send a command through CATClient directly
	cat.SetSpeed(30)

	// This should not break — both paths coexist
	time.Sleep(100 * time.Millisecond)
}

func TestFullBridgeScenario(t *testing.T) {
	// Simulate: mock radio ← CATClient ← CATServer ← remote CATClient
	radioLn, radioCmds := mockCATResponder(t, func(cmd string, conn net.Conn) {
		switch {
		case cmd == "KS":
			fmt.Fprint(conn, "KS025;")
		case cmd == "FA":
			fmt.Fprint(conn, "FA00014053284;")
		}
	})

	// "Bridge" CATClient connected to the radio
	bridge := NewCATClient()
	if err := bridge.ConnectTCP(radioLn.Addr().String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bridge.Close()
		time.Sleep(50 * time.Millisecond)
		radioLn.Close()
	})

	// CATServer exposing the bridge
	srv, err := NewCATServer(bridge, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Run()
	t.Cleanup(func() { srv.Close() })

	time.Sleep(50 * time.Millisecond)

	// "Remote" CATClient connecting through the CATServer
	remote := NewCATClient()
	if err := remote.ConnectTCP(srv.Addr()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remote.Close() })

	// Remote sets speed — should reach the radio
	remote.SetSpeed(30)
	select {
	case cmd := <-radioCmds:
		if cmd != "KS030" {
			t.Errorf("radio got %q, want KS030", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for KS030 at radio")
	}

	// Bridge CATClient can also send directly
	bridge.BandUp()
	select {
	case cmd := <-radioCmds:
		if cmd != "BU" {
			t.Errorf("radio got %q, want BU", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for BU at radio")
	}
}
