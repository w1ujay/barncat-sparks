package config

import "testing"

func TestActionRegistryHasAllActions(t *testing.T) {
	expected := []string{
		"vfo_tune", "volume", "speed", "ptt",
		"band_up", "band_down", "vfo_a", "vfo_b", "vfo_swap",
	}
	for _, name := range expected {
		if _, ok := ActionRegistry[name]; !ok {
			t.Errorf("ActionRegistry missing %q", name)
		}
	}
}

func TestActionRegistryLabels(t *testing.T) {
	def := ActionRegistry["vfo_tune"]
	if def.Label != "VFO Tune" {
		t.Errorf("vfo_tune label = %q, want 'VFO Tune'", def.Label)
	}
}

func TestActionRegistryOptions(t *testing.T) {
	def := ActionRegistry["vfo_tune"]
	if len(def.Options) != 1 {
		t.Fatalf("vfo_tune has %d options, want 1", len(def.Options))
	}
	opt := def.Options[0]
	if opt.Key != "step" {
		t.Errorf("option key = %q, want 'step'", opt.Key)
	}
	if opt.Default != "10" {
		t.Errorf("option default = %q, want '10'", opt.Default)
	}
}

func TestActionNames(t *testing.T) {
	names := ActionNames()
	if len(names) != len(ActionRegistry) {
		t.Errorf("ActionNames() returned %d, want %d", len(names), len(ActionRegistry))
	}
}

// TestEveryActionHasKind: the knob filters actions by kind, so an action
// without one would silently disappear from the knob's list.
func TestEveryActionHasKind(t *testing.T) {
	for name, def := range ActionRegistry {
		switch def.Kind {
		case KindAbsolute, KindRelative, KindButton:
		default:
			t.Errorf("action %q has kind %q, want absolute, relative or button", name, def.Kind)
		}
		if def.Momentary && def.Kind != KindButton {
			t.Errorf("action %q is momentary but kind %q; momentary actions are buttons", name, def.Kind)
		}
	}
}

func TestActionKinds(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		momentary bool
	}{
		{"vfo_tune", KindRelative, false},
		{"vfo_b_tune", KindRelative, false},
		{"rf_gain_adjust", KindRelative, false},
		{"rf_gain_b_adjust", KindRelative, false},
		{"filter_adjust", KindRelative, false},
		{"vfo_swap", KindButton, false},
		{"rit_clear", KindButton, false},
		{"rf_gain_up", KindButton, false},
		{"filter_down", KindButton, false},
		{"cw_msg", KindButton, false},
		{"ptt", KindButton, true},
		{"volume", KindAbsolute, false},
		{"speed", KindAbsolute, false},
		{"notch_freq", KindAbsolute, false},
	}
	for _, tt := range tests {
		def, ok := ActionRegistry[tt.name]
		if !ok {
			t.Errorf("ActionRegistry missing %q", tt.name)
			continue
		}
		if def.Kind != tt.kind || def.Momentary != tt.momentary {
			t.Errorf("%s: kind=%q momentary=%v, want kind=%q momentary=%v", tt.name, def.Kind, def.Momentary, tt.kind, tt.momentary)
		}
	}
}
