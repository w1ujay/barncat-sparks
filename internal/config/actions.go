package config

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
	"vfo_tune":   {Label: "VFO Tune", Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "10"}}},
	"volume":     {Label: "Volume (Main)", Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"volume_b":   {Label: "Volume (Sub)", Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"rf_gain":    {Label: "RF Gain (Main)", Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"rf_gain_b":  {Label: "RF Gain (Sub)", Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"power":      {Label: "TX Power", Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"speed":      {Label: "Keyer Speed"},
	"ptt":        {Label: "PTT"},
	"band_up":    {Label: "Band Up"},
	"band_down":  {Label: "Band Down"},
	"vfo_a":      {Label: "VFO A"},
	"vfo_b":      {Label: "VFO B"},
	"vfo_swap":   {Label: "VFO A/B Swap"},
	"vfo_a_to_b":  {Label: "VFO A → B"},
	"vfo_b_to_a":  {Label: "VFO B → A"},
	"vfo_b_tune":  {Label: "VFO B Tune", Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "10"}}},
	"rit":         {Label: "RIT Toggle"},
	"xit":         {Label: "XIT Toggle"},
	"rit_clear":   {Label: "RIT Clear"},
	"nb":          {Label: "Noise Blanker Toggle"},
	"nr":          {Label: "Noise Reduction Toggle"},
	"split":       {Label: "Split Toggle"},
	"filter_bw":   {Label: "Filter BW", Options: []OptionDef{{Key: "min", Label: "Min (Hz)", Default: "50"}, {Key: "max", Label: "Max (Hz)", Default: "3000"}}},
	"filter_adjust": {Label: "Filter BW Adjust", Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "50"}}},
	"filter_up":     {Label: "Filter Wider", Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "50"}}},
	"filter_down":   {Label: "Filter Narrower", Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "50"}}},
	"notch":       {Label: "Manual Notch Toggle"},
	"notch_freq":  {Label: "Manual Notch Freq", Options: []OptionDef{{Key: "min", Label: "Min (Hz)", Default: "550"}, {Key: "max", Label: "Max (Hz)", Default: "650"}}},
	"cw_msg":      {Label: "CW Message", Options: []OptionDef{{Key: "text", Label: "Text", Default: "CQ CQ CQ"}}},
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
