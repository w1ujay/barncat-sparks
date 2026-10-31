# Numark DJ2GO2 — Sparks MIDI Mapping Reference

Physical controls on the DJ2GO2 mapped to their MIDI messages and suggested
sparks actions. Derived from the Mixxx controller preset for this device.

The DJ2GO2 has two "decks" (A/B) — we use Deck A for main VFO / main RX
controls and Deck B for sub VFO / sub RX.  The pad modes (Hot Cue, Beat Loop,
Loop, Sampler) each send on a different MIDI channel — this gives us 4 layers
of 4 pads per deck (16 buttons per side), plus transport buttons and knobs.

## MIDI Channel Key

| Mixxx Status | MIDI Channel | sparks `channel` | Context          |
|-------------|-------------|-------------------|------------------|
| 0x90 / 0xB0 | 0           | 0                 | Deck A transport |
| 0x91 / 0xB1 | 1           | 1                 | Deck B transport |
| 0x94        | 4           | 4                 | Deck A pads (shifted layer) |
| 0x95        | 5           | 5                 | Deck B pads (shifted layer) |
| 0x9F / 0xBF | 15          | 15                | Master / Library |

Note: The DJ2GO2 sends on channels 0x84/0x85 for Note Off in pad mode (shifted
layer). Sparks only matches `note_on`, so Note Off events are handled
separately (PTT release, etc.).

## Knobs & Sliders (CC)

| Physical Control     | Type | Ch  | CC  | Mixxx Function | Suggested Sparks Action    |
|---------------------|------|-----|-----|----------------|---------------------------|
| Jog Wheel A         | cc   | 0   | 6   | wheelTurn      | `vfo_tune` (step: 10)     |
| Jog Wheel B         | cc   | 1   | 6   | wheelTurn      | `vfo_b_tune` (step: 10)   |
| Pitch Fader A       | cc   | 0   | 9   | rate           | `speed` (keyer WPM)       |
| Pitch Fader B       | cc   | 1   | 9   | rate           | `power` (TX power)        |
| Pregain Knob A      | cc   | 0   | 22  | pregain        | `rf_gain`                 |
| Pregain Knob B      | cc   | 1   | 22  | pregain        | `rf_gain_b`               |
| Crossfader          | cc   | 15  | 8   | crossfader     | `filter_bw`               |
| Browse Knob (rotate)| cc   | 15  | 0   | MoveVertical   | `filter_adjust` (step: 50)|
| Master Gain         | cc   | 15  | 10  | gain           | `volume`                  |
| Headphone Gain      | cc   | 15  | 12  | headGain       | `volume_b`                |

## Transport Buttons (Note On — channels 0/1)

| Physical Control     | Type    | Ch  | Note | Mixxx Function | Suggested Sparks Action |
|---------------------|---------|-----|------|----------------|------------------------|
| Play A              | note_on | 0   | 0    | play           | `ptt`                  |
| Play B              | note_on | 1   | 0    | play           | `split`                |
| Cue A               | note_on | 0   | 1    | cue_default    | `vfo_a`                |
| Cue B               | note_on | 1   | 1    | cue_default    | `vfo_b`                |
| Sync A              | note_on | 0   | 2    | sync_enabled   | `vfo_swap`             |
| Sync B              | note_on | 1   | 2    | sync_enabled   | `vfo_a_to_b`           |
| Headphone/PFL A     | note_on | 0   | 27   | pfl            | `nb` (noise blanker)   |
| Headphone/PFL B     | note_on | 1   | 27   | pfl            | `nr` (noise reduction) |

## Performance Pads — Hot Cue Mode (channels 4/5)

These are the 4 pads per deck in Hot Cue mode.

| Physical Control  | Type    | Ch  | Note | Suggested Sparks Action             |
|------------------|---------|-----|------|-------------------------------------|
| Pad 1 A          | note_on | 4   | 1    | `band_up`                           |
| Pad 2 A          | note_on | 4   | 2    | `band_down`                         |
| Pad 3 A          | note_on | 4   | 3    | `rit`                               |
| Pad 4 A          | note_on | 4   | 4    | `xit`                               |
| Pad 1 B          | note_on | 5   | 1    | `cw_msg` (text: "CQ CQ CQ")        |
| Pad 2 B          | note_on | 5   | 2    | `cw_msg` (text: "TU 73")           |
| Pad 3 B          | note_on | 5   | 3    | `cw_msg` (text: custom msg 3)      |
| Pad 4 B          | note_on | 5   | 4    | `cw_msg` (text: custom msg 4)      |

## Performance Pads — Beat Loop Mode (channels 4/5)

Same 4 pads in Beat Loop mode (press mode button to switch).

| Physical Control    | Type    | Ch  | Note | Suggested Sparks Action  |
|--------------------|---------|-----|------|--------------------------|
| Beat Loop 1 A      | note_on | 4   | 17   | `notch` (toggle)         |
| Beat Loop 2 A      | note_on | 4   | 18   | `rit_clear`              |
| Beat Loop 3 A      | note_on | 4   | 19   | `filter_up` (step: 100)  |
| Beat Loop 4 A      | note_on | 4   | 20   | `filter_down` (step: 100)|
| Beat Loop 1 B      | note_on | 5   | 17   | (unassigned)             |
| Beat Loop 2 B      | note_on | 5   | 18   | (unassigned)             |
| Beat Loop 3 B      | note_on | 5   | 19   | (unassigned)             |
| Beat Loop 4 B      | note_on | 5   | 20   | (unassigned)             |

## Performance Pads — Loop Mode (channels 4/5)

Same 4 pads in Loop mode (3 buttons: Loop In, Loop Out, Reloop).

| Physical Control | Type    | Ch  | Note | Suggested Sparks Action |
|-----------------|---------|-----|------|-------------------------|
| Loop In A       | note_on | 4   | 33   | `rf_gain_up` (step: 5)  |
| Loop Out A      | note_on | 4   | 34   | `rf_gain_down` (step: 5)|
| Reloop A        | note_on | 4   | 35   | `rf_gain_toggle`        |
| Loop In B       | note_on | 5   | 33   | `rf_gain_b_up` (step: 5)|
| Loop Out B      | note_on | 5   | 34   | `rf_gain_b_down` (step: 5)|
| Reloop B        | note_on | 5   | 35   | `rf_gain_b_toggle`      |

## Performance Pads — Sampler Mode (channels 4/5)

Same 4 pads in Sampler mode.

| Physical Control | Type    | Ch  | Note | Suggested Sparks Action           |
|-----------------|---------|-----|------|-----------------------------------|
| Sampler 1 A     | note_on | 4   | 49   | `vfo_b_to_a`                     |
| Sampler 2 A     | note_on | 4   | 50   | (unassigned)                      |
| Sampler 3 A     | note_on | 4   | 51   | (unassigned)                      |
| Sampler 4 A     | note_on | 4   | 52   | (unassigned)                      |
| Sampler 1 B     | note_on | 5   | 49   | (unassigned)                      |
| Sampler 2 B     | note_on | 5   | 50   | (unassigned)                      |
| Sampler 3 B     | note_on | 5   | 51   | (unassigned)                      |
| Sampler 4 B     | note_on | 5   | 52   | (unassigned)                      |

## Master Buttons (channel 15)

| Physical Control       | Type    | Ch  | Note | Suggested Sparks Action |
|-----------------------|---------|-----|------|-------------------------|
| Browse Knob (press)   | note_on | 15  | 6    | (unassigned)            |
| Load A                | note_on | 15  | 2    | (unassigned)            |
| Load B                | note_on | 15  | 3    | (unassigned)            |

## Summary: Control Count

| Category          | Controls | Assigned | Unassigned |
|-------------------|----------|----------|------------|
| Knobs/Sliders     | 10       | 10       | 0          |
| Transport Buttons | 8        | 8        | 0          |
| Hot Cue Pads      | 8        | 8        | 0          |
| Beat Loop Pads    | 8        | 4        | 4          |
| Loop Pads         | 6        | 6        | 0          |
| Sampler Pads      | 8        | 1        | 7          |
| Master Buttons    | 3        | 0        | 3          |
| **Total**         | **51**   | **37**   | **14**     |

## Example sparks-config.json mappings array

```json
[
  {"action": "vfo_tune",   "type": "cc",      "channel": 0,  "number": 6,  "opts": {"step": 10}},
  {"action": "vfo_b_tune", "type": "cc",      "channel": 1,  "number": 6,  "opts": {"step": 10}},
  {"action": "speed",      "type": "cc",      "channel": 0,  "number": 9},
  {"action": "power",      "type": "cc",      "channel": 1,  "number": 9},
  {"action": "rf_gain",    "type": "cc",      "channel": 0,  "number": 22},
  {"action": "rf_gain_b",  "type": "cc",      "channel": 1,  "number": 22},
  {"action": "filter_bw",  "type": "cc",      "channel": 15, "number": 8,  "opts": {"min": 50, "max": 3000}},
  {"action": "filter_adjust","type": "cc",    "channel": 15, "number": 0,  "opts": {"step": 50}},
  {"action": "volume",     "type": "cc",      "channel": 15, "number": 10},
  {"action": "volume_b",   "type": "cc",      "channel": 15, "number": 12},
  {"action": "ptt",        "type": "note_on", "channel": 0,  "number": 0},
  {"action": "split",      "type": "note_on", "channel": 1,  "number": 0},
  {"action": "vfo_a",      "type": "note_on", "channel": 0,  "number": 1},
  {"action": "vfo_b",      "type": "note_on", "channel": 1,  "number": 1},
  {"action": "vfo_swap",   "type": "note_on", "channel": 0,  "number": 2},
  {"action": "vfo_a_to_b", "type": "note_on", "channel": 1,  "number": 2},
  {"action": "nb",         "type": "note_on", "channel": 0,  "number": 27},
  {"action": "nr",         "type": "note_on", "channel": 1,  "number": 27},
  {"action": "band_up",    "type": "note_on", "channel": 4,  "number": 1},
  {"action": "band_down",  "type": "note_on", "channel": 4,  "number": 2},
  {"action": "rit",        "type": "note_on", "channel": 4,  "number": 3},
  {"action": "xit",        "type": "note_on", "channel": 4,  "number": 4},
  {"action": "cw_msg",     "type": "note_on", "channel": 5,  "number": 1, "opts": {"text": "CQ CQ CQ"}},
  {"action": "cw_msg",     "type": "note_on", "channel": 5,  "number": 2, "opts": {"text": "TU 73"}},
  {"action": "notch",      "type": "note_on", "channel": 4,  "number": 17},
  {"action": "rit_clear",  "type": "note_on", "channel": 4,  "number": 18},
  {"action": "filter_up",  "type": "note_on", "channel": 4,  "number": 19, "opts": {"step": 100}},
  {"action": "filter_down","type": "note_on", "channel": 4,  "number": 20, "opts": {"step": 100}},
  {"action": "rf_gain_up",    "type": "note_on", "channel": 4, "number": 33, "opts": {"step": 5}},
  {"action": "rf_gain_down",  "type": "note_on", "channel": 4, "number": 34, "opts": {"step": 5}},
  {"action": "rf_gain_toggle","type": "note_on", "channel": 4, "number": 35},
  {"action": "rf_gain_b_up",    "type": "note_on", "channel": 5, "number": 33, "opts": {"step": 5}},
  {"action": "rf_gain_b_down",  "type": "note_on", "channel": 5, "number": 34, "opts": {"step": 5}},
  {"action": "rf_gain_b_toggle","type": "note_on", "channel": 5, "number": 35},
  {"action": "vfo_b_to_a", "type": "note_on", "channel": 4,  "number": 49}
]
```
