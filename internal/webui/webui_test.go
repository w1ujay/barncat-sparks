package webui

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ujay/barncat-sparks/internal/config"
)

// freeAddr returns a loopback address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// TestStartFailureReleasesListeners: when a later subsystem fails, Start must
// roll back what it already started so a retry can bind the same ports.
func TestStartFailureReleasesListeners(t *testing.T) {
	radio, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer radio.Close()
	go func() {
		for {
			c, err := radio.Accept()
			if err != nil {
				return
			}
			go io.Copy(io.Discard, c)
		}
	}()

	catListen, rsListen := freeAddr(t), freeAddr(t)
	w := &WebUI{
		config: &config.AppConfig{
			CATTransport:      "tcp",
			CATAddress:        radio.Addr().String(),
			CATListen:         catListen,
			RadioServerListen: rsListen,
			SerialPort:        filepath.Join(t.TempDir(), "no-such-winkeyer"),
			KeyerSpeed:        25,
		},
		cfgPath:   filepath.Join(t.TempDir(), "sparks-config.json"),
		statusMsg: "Stopped",
	}
	t.Cleanup(func() {
		w.mu.Lock()
		w.stopLocked()
		w.mu.Unlock()
	})

	rec := httptest.NewRecorder()
	w.handleStart(rec, httptest.NewRequest(http.MethodPost, "/api/start", nil))

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Running {
		t.Fatal("start reported running despite the WinKeyer port failing")
	}
	if !strings.Contains(resp.Message, "Serial error") {
		t.Errorf("message %q lost the serial error", resp.Message)
	}
	for _, addr := range []string{catListen, rsListen} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Errorf("%s still bound after failed start: %v", addr, err)
			continue
		}
		ln.Close()
	}
}
