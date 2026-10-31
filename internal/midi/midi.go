package midi

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/ujay/barncat-sparks/internal/controls"
)

// MIDIConfig holds the MIDI controller configuration.
type MIDIConfig struct {
	Device   string        `json:"device"`            // legacy single device
	Devices  []string      `json:"devices,omitempty"` // multi-device
	Mappings []MIDIMapping `json:"mappings"`
}

// GetDevices returns the configured device names, merging legacy and new fields.
func (c *MIDIConfig) GetDevices() []string {
	if len(c.Devices) > 0 {
		return c.Devices
	}
	if c.Device != "" {
		return []string{c.Device}
	}
	return nil
}

// MIDIMapping maps a MIDI event to a K4 action.
type MIDIMapping struct {
	Type    string         `json:"type"`
	Channel int            `json:"channel"`
	Number  int            `json:"number"`
	Action  string         `json:"action"`
	Opts    map[string]any `json:"opts"`
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

// handleMIDIEvent processes a raw MIDI message from the platform driver and
// hands mapped messages to the executor.
func (d *MIDIDriver) handleMIDIEvent(status, data1, data2 byte) {
	if l, ok := learnedFrom(status, data1, data2); ok && d.learn.offer(l) {
		log.Printf("MIDI: learned %s ch=%d number=%d", l.Type, l.Channel, l.Number)
		return
	}

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
		d.exec.Handle(controls.Event{Action: m.Action, Opts: m.Opts, Value: int(data2)})

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
		d.exec.Handle(controls.Event{Action: m.Action, Opts: m.Opts, Value: int(data2)})

	default:
		log.Printf("MIDI: unhandled msg=0x%02X ch=%d d1=%d d2=%d", msgType, channel, data1, data2)
	}
}

// handleNoteOff turns a note off on a PTT mapping into a PTT release.
func (d *MIDIDriver) handleNoteOff(channel, number byte) {
	key := midiKey{msgType: 0x90, channel: channel, number: number}
	m, ok := d.index[key]
	if !ok || m.Action != "ptt" {
		return
	}
	d.exec.Handle(controls.Event{Action: "ptt", Release: true})
}
