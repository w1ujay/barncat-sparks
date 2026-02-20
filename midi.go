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

	case "volume":
		level := value * 255 / 127
		go d.cat.SetVolume(level)

	case "speed":
		wpm := 5 + value*94/127
		go d.cat.SetSpeed(wpm)

	case "vfo_tune":
		step := 10
		if m.Opts != nil {
			if s, ok := m.Opts["step"]; ok {
				if sf, ok := s.(float64); ok {
					step = int(sf)
				}
			}
		}
		d.tuneVFO(value, step)

	default:
		log.Printf("MIDI: unknown action %q", m.Action)
	}
}

// tuneVFO adjusts VFO A frequency relative to current.
// Uses 2's complement relative CC encoding: 1-63 = positive (CW), 65-127 = negative (CCW).
// Caches frequency locally and throttles CAT commands to avoid flooding the radio.
// Re-queries the radio if the cache is stale (>2s idle) so band changes and
// external tuning are picked up automatically.
// Must be called from the event loop goroutine (not concurrently).
func (d *MIDIDriver) tuneVFO(value, step int) {
	// 2's complement relative encoding
	var delta int
	if value >= 1 && value <= 63 {
		delta = value * step
	} else if value >= 65 && value <= 127 {
		delta = (value - 128) * step
	} else {
		return // 0 or 64 = no movement
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
