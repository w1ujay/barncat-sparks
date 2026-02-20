package main

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
