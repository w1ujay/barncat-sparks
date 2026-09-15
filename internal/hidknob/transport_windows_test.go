//go:build windows

package hidknob

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestOpenDeviceHardware reads the real knob. It runs only when
// SPARKS_KNOB_HW=1, with the knob plugged in and on the WinUSB driver:
//
//	set SPARKS_KNOB_HW=1 && go test ./internal/hidknob/ -run TestOpenDeviceHardware -v
//
// Turn or press the knob within 10 seconds of starting.
func TestOpenDeviceHardware(t *testing.T) {
	if os.Getenv("SPARKS_KNOB_HW") != "1" {
		t.Skip("set SPARKS_KNOB_HW=1 to read the real knob")
	}
	tr, st, err := openDevice()
	if err != nil {
		t.Fatalf("openDevice: state %q, %v", st, err)
	}
	defer tr.Close()

	buf := make([]byte, 64)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		n, err := tr.Read(buf)
		if errors.Is(err, errTimeout) {
			continue
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if usage, ok := DecodeReport(buf[:n]); ok {
			t.Logf("knob sent usage 0x%04X (report % x)", usage, buf[:n])
			return
		}
	}
	t.Fatal("no knob movement in 10 seconds")
}
