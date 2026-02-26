package cat

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestMuxHTTPAndCAT verifies that a RadioServer with an amp proxy correctly
// routes HTTP requests to the proxy and CAT commands to the radio handler,
// on the same TCP port.
func TestMuxHTTPAndCAT(t *testing.T) {
	// Start a mock amp HTTP server
	ampServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"path":  r.URL.Path,
			"power": 1500,
		})
	}))
	defer ampServer.Close()

	mock := &mockRadio{freqA: 14060000}
	srv, err := NewRadioServer(mock, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SetAmpProxy(ampServer.URL); err != nil {
		t.Fatal(err)
	}
	go srv.Run()
	t.Cleanup(func() { srv.Close() })

	// Test CAT: raw TCP client queries FA
	c := newTestClient(t, srv.Addr())
	defer c.close()
	c.send(t, "FA;")
	got := c.recv(t)
	if got != "FA00014060000" {
		t.Errorf("CAT query: got %q, want FA00014060000", got)
	}

	// Test HTTP: GET /power through the same port
	resp, err := http.Get(fmt.Sprintf("http://%s/power", srv.Addr()))
	if err != nil {
		t.Fatalf("HTTP GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("HTTP status: %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if result["path"] != "/power" {
		t.Errorf("proxied path: got %q, want /power", result["path"])
	}
	if result["power"] != float64(1500) {
		t.Errorf("proxied power: got %v, want 1500", result["power"])
	}
}

// TestMuxConcurrent verifies HTTP and CAT work concurrently on the same port.
func TestMuxConcurrent(t *testing.T) {
	ampServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ampServer.Close()

	mock := &mockRadio{freqA: 7030000, speed: 20}
	srv, err := NewRadioServer(mock, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SetAmpProxy(ampServer.URL); err != nil {
		t.Fatal(err)
	}
	go srv.Run()
	t.Cleanup(func() { srv.Close() })

	// Open a CAT client that stays connected
	c := newTestClient(t, srv.Addr())
	defer c.close()

	// While CAT client is connected, make HTTP requests
	for i := 0; i < 3; i++ {
		resp, err := http.Get(fmt.Sprintf("http://%s/status", srv.Addr()))
		if err != nil {
			t.Fatalf("HTTP GET #%d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("HTTP #%d status: %d", i, resp.StatusCode)
		}
	}

	// CAT still works
	c.send(t, "FA;")
	got := c.recv(t)
	if got != "FA00007030000" {
		t.Errorf("CAT after HTTP: got %q, want FA00007030000", got)
	}
}

// TestMuxCATOnlyWhenNoAmp verifies that without an amp URL configured,
// RadioServer works exactly as before with no mux overhead.
func TestMuxCATOnlyWhenNoAmp(t *testing.T) {
	mock := &mockRadio{freqA: 14060000}
	srv := setupRadioServer(t, mock)

	// Verify CAT works normally
	c := newTestClient(t, srv.Addr())
	defer c.close()

	c.send(t, "FA;")
	got := c.recv(t)
	if got != "FA00014060000" {
		t.Errorf("CAT query: got %q, want FA00014060000", got)
	}

	// HTTP should NOT work (no mux, connection treated as CAT)
	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	// Should get nothing meaningful back (not an HTTP response) or an error
	if n > 0 && string(buf[:4]) == "HTTP" {
		t.Error("got HTTP response without amp proxy configured")
	}
	_ = err // timeout or EOF expected
}

// TestIsHTTP verifies the HTTP method detection function.
func TestIsHTTP(t *testing.T) {
	tests := []struct {
		input []byte
		want  bool
	}{
		{[]byte("GET "), true},
		{[]byte("PUT "), true},
		{[]byte("POST"), true},
		{[]byte("HEAD"), true},
		{[]byte("DELE"), true},
		{[]byte("PATC"), true},
		{[]byte("OPTI"), true},
		{[]byte("FA00"), false}, // CAT command
		{[]byte("KS02"), false},
		{[]byte("MD;X"), false},
		{[]byte("TX;F"), false},
		{[]byte("GET"), false},  // too short
		{[]byte(""), false},
	}
	for _, tt := range tests {
		got := isHTTP(tt.input)
		if got != tt.want {
			t.Errorf("isHTTP(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestPrefixConn verifies that prefixConn replays the buffered bytes before
// reading from the underlying connection.
func TestPrefixConn(t *testing.T) {
	// Create a pipe to simulate a connection
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		fmt.Fprint(client, "world")
		client.Close()
	}()

	pc := &prefixConn{
		Conn: server,
		buf:  []byte("hello "),
	}

	data, err := io.ReadAll(pc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("got %q, want %q", string(data), "hello world")
	}
}
