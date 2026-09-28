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

func TestValidateRadioModel(t *testing.T) {
	tests := []struct {
		model, transport, catListen string
		wantErr                     bool
	}{
		{"k4", "tcp", "", false},
		{"k4", "serial", ":9300", false},
		{"k3", "serial", "", false},
		{"k3", "tcp", "", true},
		{"k3", "serial", ":9300", true},
		{"ic7300", "serial", "", true},
	}
	for _, tt := range tests {
		err := validateRadioModel(tt.model, tt.transport, tt.catListen)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateRadioModel(%q, %q, %q) = %v, wantErr %v",
				tt.model, tt.transport, tt.catListen, err, tt.wantErr)
		}
	}
}
