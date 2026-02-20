package main

import (
	"path/filepath"
	"testing"
)

func TestSaveAndLoadAppConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wk4-config.json")

	cfg := &AppConfig{
		SerialPort:     "COM5",
		CATAddress:     "localhost:9299",
		KeyerSpeed:     25,
		MIDIDevice:     "DJ2GO2",
		MinimizeToTray: true,
		Mappings: []MappingEntry{
			{Action: "vfo_tune", Type: "cc", Channel: 0, Number: 1, Opts: map[string]interface{}{"step": float64(10)}},
			{Action: "volume", Type: "cc", Channel: 0, Number: 7},
		},
	}

	if err := SaveAppConfig(path, cfg); err != nil {
		t.Fatalf("SaveAppConfig: %v", err)
	}

	loaded, err := LoadAppConfig(path)
	if err != nil {
		t.Fatalf("LoadAppConfig: %v", err)
	}

	if loaded.SerialPort != "COM5" {
		t.Errorf("SerialPort = %q, want COM5", loaded.SerialPort)
	}
	if loaded.CATAddress != "localhost:9299" {
		t.Errorf("CATAddress = %q, want localhost:9299", loaded.CATAddress)
	}
	if loaded.KeyerSpeed != 25 {
		t.Errorf("KeyerSpeed = %d, want 25", loaded.KeyerSpeed)
	}
	if loaded.MIDIDevice != "DJ2GO2" {
		t.Errorf("MIDIDevice = %q, want DJ2GO2", loaded.MIDIDevice)
	}
	if !loaded.MinimizeToTray {
		t.Error("MinimizeToTray = false, want true")
	}
	if len(loaded.Mappings) != 2 {
		t.Fatalf("got %d mappings, want 2", len(loaded.Mappings))
	}
	if loaded.Mappings[0].Action != "vfo_tune" {
		t.Errorf("mapping[0].Action = %q, want vfo_tune", loaded.Mappings[0].Action)
	}
	step, ok := loaded.Mappings[0].Opts["step"]
	if !ok {
		t.Fatal("vfo_tune missing opts.step")
	}
	if step.(float64) != 10 {
		t.Errorf("step = %v, want 10", step)
	}
}

func TestLoadAppConfigMissing(t *testing.T) {
	_, err := LoadAppConfig("/nonexistent/wk4-config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadAppConfigDefaults(t *testing.T) {
	cfg := DefaultAppConfig()
	if cfg.CATAddress != "localhost:9299" {
		t.Errorf("default CATAddress = %q, want localhost:9299", cfg.CATAddress)
	}
	if cfg.KeyerSpeed != 25 {
		t.Errorf("default KeyerSpeed = %d, want 25", cfg.KeyerSpeed)
	}
	if cfg.CATTransport != "tcp" {
		t.Errorf("default CATTransport = %q, want tcp", cfg.CATTransport)
	}
	if cfg.CATSerialBaud != 38400 {
		t.Errorf("default CATSerialBaud = %d, want 38400", cfg.CATSerialBaud)
	}
}

func TestAppConfigBridgeFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wk4-config.json")

	cfg := &AppConfig{
		CATAddress:    "localhost:9299",
		CATTransport:  "serial",
		CATSerialPort: "COM3",
		CATSerialBaud: 38400,
		CATListen:     ":9300",
		KeyerSpeed:    25,
	}

	if err := SaveAppConfig(path, cfg); err != nil {
		t.Fatalf("SaveAppConfig: %v", err)
	}

	loaded, err := LoadAppConfig(path)
	if err != nil {
		t.Fatalf("LoadAppConfig: %v", err)
	}

	if loaded.CATTransport != "serial" {
		t.Errorf("CATTransport = %q, want serial", loaded.CATTransport)
	}
	if loaded.CATSerialPort != "COM3" {
		t.Errorf("CATSerialPort = %q, want COM3", loaded.CATSerialPort)
	}
	if loaded.CATSerialBaud != 38400 {
		t.Errorf("CATSerialBaud = %d, want 38400", loaded.CATSerialBaud)
	}
	if loaded.CATListen != ":9300" {
		t.Errorf("CATListen = %q, want :9300", loaded.CATListen)
	}
}

func TestAppConfigToMIDIConfig(t *testing.T) {
	cfg := &AppConfig{
		MIDIDevice: "DJ2GO2",
		Mappings: []MappingEntry{
			{Action: "volume", Type: "cc", Channel: 0, Number: 7},
			{Action: "ptt", Type: "note_on", Channel: 0, Number: 36},
		},
	}
	mc := cfg.ToMIDIConfig()
	if mc.Device != "DJ2GO2" {
		t.Errorf("Device = %q, want DJ2GO2", mc.Device)
	}
	if len(mc.Mappings) != 2 {
		t.Fatalf("got %d mappings, want 2", len(mc.Mappings))
	}
	if mc.Mappings[0].Action != "volume" {
		t.Errorf("mapping[0].Action = %q, want volume", mc.Mappings[0].Action)
	}
}
