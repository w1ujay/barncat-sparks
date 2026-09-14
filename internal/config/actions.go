package config

import "sort"

// Action kinds. A relative action takes a signed step (MIDI relative CC, knob
// turns); an absolute action takes a 0-127 position (sliders, knobs with
// travel); a button action fires on a press.
const (
	KindAbsolute = "absolute"
	KindRelative = "relative"
	KindButton   = "button"
)

// OptionDef describes a configurable option for an action.
type OptionDef struct {
	Key     string // JSON key in opts map (e.g., "step")
	Label   string // Human-readable label (e.g., "Step (Hz)")
	Default string // Default value as string
}

// ActionDef describes an action and its configurable options.
type ActionDef struct {
	Label     string      // Human-readable name (e.g., "VFO Tune")
	Kind      string      // KindAbsolute, KindRelative or KindButton
	Momentary bool        // acts on press and release (PTT, paddles); needs a control that reports a hold
	Options   []OptionDef // Action-specific options (empty if none)
}

// ActionRegistry maps action names to their definitions.
// To add a new action: add it here with its kind, then add the handler in controls/executor.go.
var ActionRegistry = map[string]ActionDef{
	"vfo_tune":         {Label: "VFO Tune", Kind: KindRelative, Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "10"}}},
	"volume":           {Label: "Volume (Main)", Kind: KindAbsolute, Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"volume_b":         {Label: "Volume (Sub)", Kind: KindAbsolute, Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"rf_gain":          {Label: "RF Gain (Main)", Kind: KindAbsolute, Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"rf_gain_b":        {Label: "RF Gain (Sub)", Kind: KindAbsolute, Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"rf_gain_adjust":   {Label: "RF Gain Adjust (Main)", Kind: KindRelative, Options: []OptionDef{{Key: "step", Label: "Step (dB)", Default: "1"}}},
	"rf_gain_b_adjust": {Label: "RF Gain Adjust (Sub)", Kind: KindRelative, Options: []OptionDef{{Key: "step", Label: "Step (dB)", Default: "1"}}},
	"rf_gain_up":       {Label: "RF Gain Up (Main)", Kind: KindButton, Options: []OptionDef{{Key: "step", Label: "Step (dB)", Default: "1"}}},
	"rf_gain_down":     {Label: "RF Gain Down (Main)", Kind: KindButton, Options: []OptionDef{{Key: "step", Label: "Step (dB)", Default: "1"}}},
	"rf_gain_b_up":     {Label: "RF Gain Up (Sub)", Kind: KindButton, Options: []OptionDef{{Key: "step", Label: "Step (dB)", Default: "1"}}},
	"rf_gain_b_down":   {Label: "RF Gain Down (Sub)", Kind: KindButton, Options: []OptionDef{{Key: "step", Label: "Step (dB)", Default: "1"}}},
	"rf_gain_toggle":   {Label: "RF Gain Toggle (Main)", Kind: KindButton},
	"rf_gain_b_toggle": {Label: "RF Gain Toggle (Sub)", Kind: KindButton},
	"power":            {Label: "TX Power", Kind: KindAbsolute, Options: []OptionDef{{Key: "invert", Label: "Invert", Default: "0"}}},
	"speed":            {Label: "Keyer Speed", Kind: KindAbsolute},
	"ptt":              {Label: "PTT", Kind: KindButton, Momentary: true},
	"band_up":          {Label: "Band Up", Kind: KindButton},
	"band_down":        {Label: "Band Down", Kind: KindButton},
	"vfo_a":            {Label: "VFO A", Kind: KindButton},
	"vfo_b":            {Label: "VFO B", Kind: KindButton},
	"vfo_swap":         {Label: "VFO A/B Swap", Kind: KindButton},
	"vfo_a_to_b":       {Label: "VFO A → B", Kind: KindButton},
	"vfo_b_to_a":       {Label: "VFO B → A", Kind: KindButton},
	"vfo_b_tune":       {Label: "VFO B Tune", Kind: KindRelative, Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "10"}}},
	"rit":              {Label: "RIT Toggle", Kind: KindButton},
	"xit":              {Label: "XIT Toggle", Kind: KindButton},
	"rit_clear":        {Label: "RIT Clear", Kind: KindButton},
	"nb":               {Label: "Noise Blanker Toggle", Kind: KindButton},
	"nr":               {Label: "Noise Reduction Toggle", Kind: KindButton},
	"split":            {Label: "Split Toggle", Kind: KindButton},
	"filter_bw":        {Label: "Filter BW", Kind: KindAbsolute, Options: []OptionDef{{Key: "min", Label: "Min (Hz)", Default: "50"}, {Key: "max", Label: "Max (Hz)", Default: "3000"}}},
	"filter_adjust":    {Label: "Filter BW Adjust", Kind: KindRelative, Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "50"}}},
	"filter_up":        {Label: "Filter Wider", Kind: KindButton, Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "50"}}},
	"filter_down":      {Label: "Filter Narrower", Kind: KindButton, Options: []OptionDef{{Key: "step", Label: "Step (Hz)", Default: "50"}}},
	"notch":            {Label: "Manual Notch Toggle", Kind: KindButton},
	"notch_freq":       {Label: "Manual Notch Freq", Kind: KindAbsolute, Options: []OptionDef{{Key: "min", Label: "Min (Hz)", Default: "550"}, {Key: "max", Label: "Max (Hz)", Default: "650"}}},
	"cw_msg":           {Label: "CW Message", Kind: KindButton, Options: []OptionDef{{Key: "text", Label: "Text", Default: "CQ CQ CQ"}}},
	"dit_paddle":       {Label: "Dit Paddle (KZ)", Kind: KindButton, Momentary: true},
	"dah_paddle":       {Label: "Dah Paddle (KZ)", Kind: KindButton, Momentary: true},
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
