package main

import "testing"

// TestHasHeadlessWork: headless mode runs when there is anything to serve. A
// CATServer bridge or a RadioServer on its own is a complete job, not a
// reason to print usage and exit.
func TestHasHeadlessWork(t *testing.T) {
	tests := []struct {
		name                                     string
		port, midiDevice, catListen, radioListen string
		want                                     bool
	}{
		{"nothing to run", "", "", "", "", false},
		{"WinKeyer port", "COM5", "", "", "", true},
		{"MIDI device", "", "DJ2GO2", "", "", true},
		{"CATServer bridge only", "", "", ":9300", "", true},
		{"RadioServer only", "", "", "", "127.0.0.1:9400", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasHeadlessWork(tt.port, tt.midiDevice, tt.catListen, tt.radioListen); got != tt.want {
				t.Fatalf("hasHeadlessWork = %v, want %v", got, tt.want)
			}
		})
	}
}
