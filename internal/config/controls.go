package config

// ControlMap maps MIDI "type:channel:number" keys to physical control names.
// Keys use the format "cc:0:6" or "note_on:4:1".
type ControlMap map[string]string

// ControlMaps holds known control maps keyed by device name substring.
// When rendering, the UI checks whether any configured device name contains
// the key string (e.g., a device named "Numark DJ2GO2 Touch" matches "DJ2GO2").
var ControlMaps = map[string]ControlMap{
	"DJ2GO2": dj2go2Controls,
}

// Numark DJ2GO2 — derived from Mixxx controller preset (docs/Numark-DJ2Go2-midi-map.xml).
// Deck A = channel 0, Deck B = channel 1, Master/Library = channel 15.
// Pad modes use channels 4 (Deck A) and 5 (Deck B).
var dj2go2Controls = ControlMap{
	// Knobs & sliders
	"cc:0:6":   "Jog Wheel A",
	"cc:1:6":   "Jog Wheel B",
	"cc:0:9":   "Pitch Fader A",
	"cc:1:9":   "Pitch Fader B",
	"cc:0:22":  "Pregain Knob A",
	"cc:1:22":  "Pregain Knob B",
	"cc:15:8":  "Crossfader",
	"cc:15:0":  "Browse Knob",
	"cc:15:10": "Master Gain",
	"cc:15:12": "Headphone Gain",

	// Transport buttons (Deck A = ch 0, Deck B = ch 1)
	"note_on:0:0":  "Play A",
	"note_on:1:0":  "Play B",
	"note_on:0:1":  "Cue A",
	"note_on:1:1":  "Cue B",
	"note_on:0:2":  "Sync A",
	"note_on:1:2":  "Sync B",
	"note_on:0:27": "PFL A",
	"note_on:1:27": "PFL B",

	// Hot Cue pads (ch 4 = Deck A, ch 5 = Deck B)
	"note_on:4:1": "Hot Cue 1 A",
	"note_on:5:1": "Hot Cue 1 B",
	"note_on:4:2": "Hot Cue 2 A",
	"note_on:5:2": "Hot Cue 2 B",
	"note_on:4:3": "Hot Cue 3 A",
	"note_on:5:3": "Hot Cue 3 B",
	"note_on:4:4": "Hot Cue 4 A",
	"note_on:5:4": "Hot Cue 4 B",

	// Beat Loop pads
	"note_on:4:17": "Beat Loop 1 A",
	"note_on:5:17": "Beat Loop 1 B",
	"note_on:4:18": "Beat Loop 2 A",
	"note_on:5:18": "Beat Loop 2 B",
	"note_on:4:19": "Beat Loop 3 A",
	"note_on:5:19": "Beat Loop 3 B",
	"note_on:4:20": "Beat Loop 4 A",
	"note_on:5:20": "Beat Loop 4 B",

	// Loop pads
	"note_on:4:33": "Loop In A",
	"note_on:5:33": "Loop In B",
	"note_on:4:34": "Loop Out A",
	"note_on:5:34": "Loop Out B",
	"note_on:4:35": "Reloop A",
	"note_on:5:35": "Reloop B",

	// Sampler pads
	"note_on:4:49": "Sampler 1 A",
	"note_on:5:49": "Sampler 1 B",
	"note_on:4:50": "Sampler 2 A",
	"note_on:5:50": "Sampler 2 B",
	"note_on:4:51": "Sampler 3 A",
	"note_on:5:51": "Sampler 3 B",
	"note_on:4:52": "Sampler 4 A",
	"note_on:5:52": "Sampler 4 B",

	// Master buttons (ch 15)
	"note_on:15:6": "Browse Press",
	"note_on:15:2": "Load A",
	"note_on:15:3": "Load B",
}

// KnobControlNames names the gestures of the DROK/iWit knob as configured on
// the user's unit (usage codes captured over WinUSB). Keys match Knob rows:
// "knob:<report ID>:<usage decimal>". Another unit may send other codes; its
// rows then show "Code 0x…" instead of a name.
var KnobControlNames = ControlMap{
	"knob:3:183": "Turn Right",         // 0xB7
	"knob:3:234": "Turn Left",          // 0xEA
	"knob:3:226": "Press",              // 0xE2
	"knob:3:181": "Press + Turn Right", // 0xB5
	"knob:3:233": "Press + Turn Left",  // 0xE9 (sent twice per click)
	"knob:3:205": "Long Press",         // 0xCD
}
