package midi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMIDIConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "midi-config.json")
	data := []byte(`{
		"device": "DJ2GO2",
		"mappings": [
			{"type": "cc", "channel": 0, "number": 7, "action": "volume"},
			{"type": "note_on", "channel": 0, "number": 36, "action": "ptt"},
			{"type": "cc", "channel": 0, "number": 1, "action": "vfo_tune", "opts": {"step": 100}}
		]
	}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadMIDIConfig(path)
	if err != nil {
		t.Fatalf("LoadMIDIConfig: %v", err)
	}
	if cfg.Device != "DJ2GO2" {
		t.Errorf("device = %q, want DJ2GO2", cfg.Device)
	}
	if len(cfg.Mappings) != 3 {
		t.Fatalf("got %d mappings, want 3", len(cfg.Mappings))
	}
	m := cfg.Mappings[2]
	if m.Action != "vfo_tune" {
		t.Errorf("mapping[2].action = %q, want vfo_tune", m.Action)
	}
	step, ok := m.Opts["step"]
	if !ok {
		t.Fatal("vfo_tune missing opts.step")
	}
	if step.(float64) != 100 {
		t.Errorf("step = %v, want 100", step)
	}
}

func TestLoadMIDIConfigMissing(t *testing.T) {
	_, err := LoadMIDIConfig("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestBuildMappingIndex(t *testing.T) {
	cfg := &MIDIConfig{
		Mappings: []MIDIMapping{
			{Type: "cc", Channel: 0, Number: 7, Action: "volume"},
			{Type: "note_on", Channel: 1, Number: 36, Action: "ptt"},
		},
	}
	idx := buildMappingIndex(cfg)
	key := midiKey{msgType: 0xB0, channel: 0, number: 7}
	m, ok := idx[key]
	if !ok {
		t.Fatal("missing CC mapping")
	}
	if m.Action != "volume" {
		t.Errorf("action = %q, want volume", m.Action)
	}
	key = midiKey{msgType: 0x90, channel: 1, number: 36}
	m, ok = idx[key]
	if !ok {
		t.Fatal("missing note_on mapping")
	}
	if m.Action != "ptt" {
		t.Errorf("action = %q, want ptt", m.Action)
	}
}
