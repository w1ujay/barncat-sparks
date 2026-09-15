package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ujay/barncat-sparks/internal/hidknob"
)

func TestKnobEnabledRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	if err := SaveAppConfig(path, &AppConfig{KnobEnabled: true}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadAppConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.KnobEnabled {
		t.Error("KnobEnabled = false after round trip, want true")
	}
}

// TestKnobOffInOldConfigs: a config saved before the knob existed loads with
// the knob off.
func TestKnobOffInOldConfigs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	if err := os.WriteFile(path, []byte(`{"cat_address":"localhost:7373","keyer_speed":28}`), 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadAppConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.KnobEnabled {
		t.Error("KnobEnabled = true for a config without knob_enabled, want false")
	}
}

func TestToMIDIConfigSkipsKnobRows(t *testing.T) {
	cfg := &AppConfig{Mappings: []MappingEntry{
		{Action: "volume", Type: "cc", Channel: 0, Number: 7},
		{Action: "vfo_tune", Type: "knob", Channel: 3, Number: 183, Opts: map[string]any{"direction": float64(1)}},
		{Action: "ptt", Type: "note_on", Channel: 0, Number: 36},
	}}
	mc := cfg.ToMIDIConfig()
	if len(mc.Mappings) != 2 || mc.Mappings[0].Type != "cc" || mc.Mappings[1].Type != "note_on" {
		t.Fatalf("MIDI mappings = %+v, want only the cc and note_on rows", mc.Mappings)
	}
}

func TestToKnobMappings(t *testing.T) {
	up := map[string]any{"step": float64(10), "direction": float64(1)}
	down := map[string]any{"step": float64(10), "direction": float64(-1)}
	cfg := &AppConfig{Mappings: []MappingEntry{
		{Action: "volume", Type: "cc", Channel: 0, Number: 7},                                                    // row 1: MIDI, ignored
		{Action: "vfo_tune", Type: "knob", Channel: 3, Number: 183, Opts: up},                                    // row 2: ok
		{Action: "vfo_tune", Type: "knob", Channel: 3, Number: 234, Opts: down},                                  // row 3: ok
		{Action: "vfo_swap", Type: "knob", Channel: 3, Number: 226},                                              // row 4: ok
		{Action: "ptt", Type: "knob", Channel: 3, Number: 205},                                                   // row 5: momentary
		{Action: "volume", Type: "knob", Channel: 3, Number: 181},                                                // row 6: absolute
		{Action: "rf_gain_adjust", Type: "knob", Channel: 3, Number: 233, Opts: map[string]any{"step": 1.0}},     // row 7: no direction
		{Action: "filter_adjust", Type: "knob", Channel: 3, Number: 233, Opts: map[string]any{"direction": 2.0}}, // row 8: bad direction
		{Action: "", Type: "knob", Channel: 3, Number: 183},                                                      // row 9: no action
		{Action: "warp_drive", Type: "knob", Channel: 3, Number: 183},                                            // row 10: unknown action
		{Action: "rit_clear", Type: "knob", Channel: 3, Number: 0},                                               // row 11: not learned
	}}

	maps, skipped := cfg.ToKnobMappings()

	wantMaps := []hidknob.Mapping{
		{Usage: 183, Action: "vfo_tune", Opts: up, Direction: 1},
		{Usage: 234, Action: "vfo_tune", Opts: down, Direction: -1},
		{Usage: 226, Action: "vfo_swap", Direction: 0},
	}
	if !reflect.DeepEqual(maps, wantMaps) {
		t.Errorf("mappings = %+v\nwant       %+v", maps, wantMaps)
	}
	wantSkipped := []string{
		"row 5: PTT can't be used on the knob",
		"row 6: Volume (Main) needs a slider, not the knob",
		"row 7: RF Gain Adjust (Main) needs a direction (+ or −)",
		"row 8: Filter BW Adjust needs a direction (+ or −)",
		"row 9: no action chosen",
		`row 10: unknown action "warp_drive"`,
		"row 11: knob gesture not learned yet",
	}
	if !reflect.DeepEqual(skipped, wantSkipped) {
		t.Errorf("skipped = %q\nwant      %q", skipped, wantSkipped)
	}
}

// TestKnobRowSurvivesSaveLoad: a Knob row saved with plain-int opts (as the
// web UI's JSON does) still yields a usable mapping after a save/load round
// trip, once JSON has turned those ints into float64.
func TestKnobRowSurvivesSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	cfg := &AppConfig{Mappings: []MappingEntry{
		{Action: "vfo_tune", Type: "knob", Channel: 3, Number: 234, Opts: map[string]any{"step": 10, "direction": -1}},
	}}
	if err := SaveAppConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadAppConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	maps, skipped := loaded.ToKnobMappings()
	if len(skipped) != 0 {
		t.Fatalf("skipped = %q, want none", skipped)
	}
	if len(maps) != 1 || maps[0].Usage != 234 || maps[0].Action != "vfo_tune" || maps[0].Direction != -1 {
		t.Fatalf("mappings = %+v, want one mapping usage 234 action vfo_tune direction -1", maps)
	}
}

func TestKnobControlNames(t *testing.T) {
	want := ControlMap{
		"knob:3:183": "Turn Right",
		"knob:3:234": "Turn Left",
		"knob:3:226": "Press",
		"knob:3:181": "Press + Turn Right",
		"knob:3:233": "Press + Turn Left",
		"knob:3:205": "Long Press",
	}
	if !reflect.DeepEqual(KnobControlNames, want) {
		t.Errorf("KnobControlNames = %v, want %v", KnobControlNames, want)
	}
}
