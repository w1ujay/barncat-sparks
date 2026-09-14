package hidknob

import (
	"testing"

	"github.com/ujay/barncat-sparks/internal/controls"
)

func TestDecodeReport(t *testing.T) {
	tests := []struct {
		name   string
		report []byte
		usage  uint16
		ok     bool
	}{
		{"turn right", []byte{0x03, 0xb7, 0x00}, 0x00B7, true},
		{"long press", []byte{0x03, 0xcd, 0x00}, 0x00CD, true},
		{"little-endian usage", []byte{0x03, 0x34, 0x02}, 0x0234, true},
		{"release", []byte{0x03, 0x00, 0x00}, 0, false},
		{"mouse report", []byte{0x01, 0x00, 0x00, 0x00, 0x01}, 0, false},
		{"keyboard report", []byte{0x02, 0x00, 0x00, 0x3c, 0x00, 0x00, 0x00, 0x00, 0x00}, 0, false},
		{"dial report", []byte{0x0a, 0x01, 0x00, 0x00}, 0, false},
		{"short packet", []byte{0x03, 0xb7}, 0, false},
		{"empty", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage, ok := DecodeReport(tt.report)
			if usage != tt.usage || ok != tt.ok {
				t.Fatalf("DecodeReport(% x) = 0x%04X, %v; want 0x%04X, %v", tt.report, usage, ok, tt.usage, tt.ok)
			}
		})
	}
}

func TestEventForMapping(t *testing.T) {
	step := map[string]any{"step": float64(10)}
	tests := []struct {
		name string
		m    Mapping
		want controls.Event
	}{
		{"relative up", Mapping{Usage: 0xB7, Action: "vfo_tune", Opts: step, Direction: 1}, controls.Event{Action: "vfo_tune", Opts: step, Value: 1}},
		{"relative down", Mapping{Usage: 0xEA, Action: "vfo_tune", Opts: step, Direction: -1}, controls.Event{Action: "vfo_tune", Opts: step, Value: 127}},
		{"button", Mapping{Usage: 0xE2, Action: "vfo_swap"}, controls.Event{Action: "vfo_swap", Value: 127}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eventFor(tt.m)
			if got.Action != tt.want.Action || got.Value != tt.want.Value || got.Release || len(got.Opts) != len(tt.want.Opts) {
				t.Fatalf("eventFor(%+v) = %+v, want %+v", tt.m, got, tt.want)
			}
		})
	}
}
