package main

import "sort"

// OptionDef describes a configurable option for an action.
type OptionDef struct {
	Key     string // JSON key in opts map (e.g., "step")
	Label   string // Human-readable label (e.g., "Step (Hz)")
	Default string // Default value as string
}

// ActionDef describes a MIDI action and its configurable options.
type ActionDef struct {
	Label   string      // Human-readable name (e.g., "VFO Tune")
	Options []OptionDef // Action-specific options (empty if none)
}

// ActionRegistry maps action names to their definitions.
// To add a new action: add it here, then add the handler in midi.go dispatch().
var ActionRegistry = map[string]ActionDef{
	"vfo_tune":  {Label: "VFO Tune", Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "10"}}},
	"volume":    {Label: "Volume"},
	"speed":     {Label: "Speed"},
	"ptt":       {Label: "PTT"},
	"band_up":   {Label: "Band Up"},
	"band_down": {Label: "Band Down"},
	"vfo_a":     {Label: "VFO A"},
	"vfo_b":     {Label: "VFO B"},
	"vfo_swap":  {Label: "VFO Swap"},
}

// ActionNames returns sorted action names for populating dropdowns.
func ActionNames() []string {
	names := make([]string, 0, len(ActionRegistry))
	for name := range ActionRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
