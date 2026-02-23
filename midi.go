package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

// MIDIConfig holds the MIDI controller configuration.
type MIDIConfig struct {
	Device   string        `json:"device"`
	Mappings []MIDIMapping `json:"mappings"`
}

// MIDIMapping maps a MIDI event to a K4 action.
type MIDIMapping struct {
	Type    string                 `json:"type"`
	Channel int                    `json:"channel"`
	Number  int                    `json:"number"`
	Action  string                 `json:"action"`
	Opts    map[string]interface{} `json:"opts"`
}

// LoadMIDIConfig reads and parses a MIDI config JSON file.
func LoadMIDIConfig(path string) (*MIDIConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read MIDI config: %w", err)
	}
	var cfg MIDIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse MIDI config: %w", err)
	}
	return &cfg, nil
}

// midiKey uniquely identifies a MIDI event for fast lookup.
type midiKey struct {
	msgType byte
	channel byte
	number  byte
}

// buildMappingIndex creates a lookup map from MIDI event to mapping.
func buildMappingIndex(cfg *MIDIConfig) map[midiKey]*MIDIMapping {
	idx := make(map[midiKey]*MIDIMapping, len(cfg.Mappings))
	for i := range cfg.Mappings {
		m := &cfg.Mappings[i]
		var msgType byte
		switch m.Type {
		case "cc":
			msgType = 0xB0
		case "note_on":
			msgType = 0x90
		default:
			log.Printf("MIDI: unknown mapping type %q, skipping", m.Type)
			continue
		}
		key := midiKey{
			msgType: msgType,
			channel: byte(m.Channel),
			number:  byte(m.Number),
		}
		idx[key] = m
	}
	return idx
}

// handleMIDIEvent processes a raw MIDI message from the platform driver.
func (d *MIDIDriver) handleMIDIEvent(status, data1, data2 byte) {
	msgType := status & 0xF0
	channel := status & 0x0F

	switch msgType {
	case 0x90: // Note On
		if data2 == 0 {
			d.handleNoteOff(channel, data1)
			return
		}
		key := midiKey{msgType: 0x90, channel: channel, number: data1}
		m, ok := d.index[key]
		if !ok {
			log.Printf("MIDI: unmapped note_on ch=%d note=%d vel=%d", channel, data1, data2)
			return
		}
		log.Printf("MIDI: note_on ch=%d note=%d vel=%d -> %s", channel, data1, data2, m.Action)
		d.dispatch(m, int(data2))

	case 0x80: // Note Off
		d.handleNoteOff(channel, data1)

	case 0xB0: // Control Change
		key := midiKey{msgType: 0xB0, channel: channel, number: data1}
		m, ok := d.index[key]
		if !ok {
			log.Printf("MIDI: unmapped cc ch=%d cc=%d val=%d", channel, data1, data2)
			return
		}
		log.Printf("MIDI: cc ch=%d cc=%d val=%d -> %s", channel, data1, data2, m.Action)
		d.dispatch(m, int(data2))

	default:
		log.Printf("MIDI: unhandled msg=0x%02X ch=%d d1=%d d2=%d", msgType, channel, data1, data2)
	}
}

// handleNoteOff processes note off events (used for PTT release).
func (d *MIDIDriver) handleNoteOff(channel, number byte) {
	key := midiKey{msgType: 0x90, channel: channel, number: number}
	m, ok := d.index[key]
	if !ok {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if m.Action == "ptt" && d.pttOn {
		log.Printf("MIDI: note_off ch=%d note=%d -> ptt off", channel, number)
		d.pttOn = false
		go d.cat.SetPTT(false)
	}
}

// optFloat returns a float64 option from a mapping's opts map, or the fallback.
func optFloat(opts map[string]interface{}, key string, fallback float64) float64 {
	if opts == nil {
		return fallback
	}
	v, ok := opts[key]
	if !ok {
		return fallback
	}
	f, ok := v.(float64)
	if !ok {
		return fallback
	}
	return f
}

// ccValue scales a MIDI CC value (0-127) to an output range, with optional invert.
func ccValue(value, maxOut int, opts map[string]interface{}) int {
	if optFloat(opts, "invert", 0) != 0 {
		value = 127 - value
	}
	return value * maxOut / 127
}

// dispatch executes the action for a MIDI event.
func (d *MIDIDriver) dispatch(m *MIDIMapping, value int) {
	switch m.Action {
	case "ptt":
		d.mu.Lock()
		d.pttOn = true
		d.mu.Unlock()
		go d.cat.SetPTT(true)

	case "band_up":
		go d.cat.BandUp()

	case "band_down":
		go d.cat.BandDown()

	case "vfo_a":
		go d.cat.SelectVFO(0)

	case "vfo_b":
		go d.cat.SelectVFO(1)

	case "vfo_swap":
		go d.cat.SwapVFO()

	case "vfo_a_to_b":
		go d.cat.VFOAtoB()

	case "vfo_b_to_a":
		go d.cat.VFOBtoA()

	case "volume":
		level := ccValue(value, 60, m.Opts)
		go d.cat.SetVolume(0, level)

	case "volume_b":
		level := ccValue(value, 60, m.Opts)
		go d.cat.SetVolume(1, level)

	case "rf_gain":
		level := ccValue(value, 60, m.Opts)
		go d.cat.SetRFGain(0, level)

	case "rf_gain_b":
		level := ccValue(value, 60, m.Opts)
		go d.cat.SetRFGain(1, level)

	case "power":
		v := value
		if optFloat(m.Opts, "invert", 0) != 0 {
			v = 127 - v
		}
		// CC 0-100: 0.1W to 10.0W (0.1W per step, QRP range)
		// CC 101-127: ~11W to 110W (~3.7W per step, QRO range)
		if v <= 100 {
			tenths := v + 1 // 1-101 → 0.1W-10.1W, clamped to 100 max
			if tenths > 100 {
				tenths = 100
			}
			go d.cat.SetPower(tenths)
		} else {
			watts := 11 + (v-101)*99/26 // 11W-110W
			go d.cat.SetPower(watts * 10)
		}

	case "speed":
		wpm := 5 + value*94/127
		go d.cat.SetSpeed(wpm)

	case "rit":
		d.ritOn = !d.ritOn
		go d.cat.SetRIT(d.ritOn)

	case "xit":
		d.xitOn = !d.xitOn
		go d.cat.SetXIT(d.xitOn)

	case "rit_clear":
		d.ritOn = false
		go d.cat.ClearRIT()

	case "nb":
		d.nbOn = !d.nbOn
		go d.cat.SetNB(d.nbOn)

	case "nr":
		d.nrOn = !d.nrOn
		go d.cat.SetNR(d.nrOn)

	case "split":
		d.splitOn = !d.splitOn
		go d.cat.SetSplit(d.splitOn)

	case "vfo_tune":
		step := int(optFloat(m.Opts, "step", 10))
		d.tuneVFO(value, step)

	case "vfo_b_tune":
		step := int(optFloat(m.Opts, "step", 10))
		d.tuneVFOB(value, step)

	case "filter_bw":
		minHz := int(optFloat(m.Opts, "min", 50))
		maxHz := int(optFloat(m.Opts, "max", 3000))
		bw := minHz + value*(maxHz-minHz)/127
		go d.cat.SetFilterBW(bw)

	case "filter_adjust":
		step := int(optFloat(m.Opts, "step", 50))
		delta := relativeCC(value, step)
		if delta != 0 {
			d.adjustFilter(delta)
		}

	case "filter_up":
		step := int(optFloat(m.Opts, "step", 50))
		d.adjustFilter(step)

	case "filter_down":
		step := int(optFloat(m.Opts, "step", 50))
		d.adjustFilter(-step)

	case "notch":
		d.notchOn = !d.notchOn
		go d.cat.SetNotch(d.notchOn)

	case "notch_freq":
		minHz := int(optFloat(m.Opts, "min", 150))
		maxHz := int(optFloat(m.Opts, "max", 3000))
		hz := minHz + value*(maxHz-minHz)/127
		d.notchOn = true
		d.notchHz = hz
		go d.cat.SetNotchFreq(hz)

	case "cw_msg":
		if m.Opts != nil {
			if text, ok := m.Opts["text"]; ok {
				if s, ok := text.(string); ok && s != "" {
					go d.cat.SendCWMessage(s)
				}
			}
		}

	default:
		log.Printf("MIDI: unknown action %q", m.Action)
	}
}

// relativeCC decodes a 2's complement relative CC value.
// 1-63 = positive (CW), 65-127 = negative (CCW), 0/64 = no movement.
func relativeCC(value, step int) int {
	if value >= 1 && value <= 63 {
		return value * step
	} else if value >= 65 && value <= 127 {
		return (value - 128) * step
	}
	return 0
}

// tuneVFO adjusts VFO A frequency relative to current.
// Caches frequency locally and throttles CAT commands to avoid flooding the radio.
// Re-queries the radio if the cache is stale (>2s idle) so band changes and
// external tuning are picked up automatically.
// Must be called from the event loop goroutine (not concurrently).
func (d *MIDIDriver) tuneVFO(value, step int) {
	delta := relativeCC(value, step)
	if delta == 0 {
		return
	}

	now := time.Now()

	// Re-query radio if cache is stale (frequency may have changed externally)
	if !d.vfoKnown || now.Sub(d.lastTuneAt) > 2*time.Second {
		hz, err := d.cat.GetFreqA()
		if err != nil {
			log.Printf("MIDI: GetFreqA failed: %v", err)
			return
		}
		d.vfoFreq = hz
		d.vfoKnown = true
	}

	d.vfoFreq += delta
	if d.vfoFreq < 0 {
		d.vfoFreq = 0
	}

	// Throttle: max ~20 commands/sec to the radio
	if now.Sub(d.lastTuneAt) < 50*time.Millisecond {
		return
	}
	d.lastTuneAt = now
	d.cat.SetFreqA(d.vfoFreq)
}

// tuneVFOB adjusts VFO B frequency relative to current.
// Same caching/throttling as tuneVFO but for VFO B.
func (d *MIDIDriver) tuneVFOB(value, step int) {
	delta := relativeCC(value, step)
	if delta == 0 {
		return
	}

	now := time.Now()

	if !d.vfoBKnown || now.Sub(d.lastTuneBAt) > 2*time.Second {
		hz, err := d.cat.GetFreqB()
		if err != nil {
			log.Printf("MIDI: GetFreqB failed: %v", err)
			return
		}
		d.vfoBFreq = hz
		d.vfoBKnown = true
	}

	d.vfoBFreq += delta
	if d.vfoBFreq < 0 {
		d.vfoBFreq = 0
	}

	if now.Sub(d.lastTuneBAt) < 50*time.Millisecond {
		return
	}
	d.lastTuneBAt = now
	d.cat.SetFreqB(d.vfoBFreq)
}

// adjustFilter changes the filter bandwidth by deltaHz.
// Tracks current BW locally (defaults to 500 Hz if unknown).
func (d *MIDIDriver) adjustFilter(deltaHz int) {
	if d.filterBW == 0 {
		d.filterBW = 500 // sensible CW default
	}
	d.filterBW += deltaHz
	if d.filterBW < 50 {
		d.filterBW = 50
	}
	if d.filterBW > 4000 {
		d.filterBW = 4000
	}
	go d.cat.SetFilterBW(d.filterBW)
}
