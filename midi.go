package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	rtmidi "github.com/mattrtaylor/go-rtmidi"
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

// MIDIDriver connects to a MIDI device and dispatches actions to the K4.
type MIDIDriver struct {
	mu     sync.Mutex
	cat    *CATClient
	config *MIDIConfig
	index  map[midiKey]*MIDIMapping
	midiIn rtmidi.MIDIIn
	pttOn  bool
	done   chan struct{}
}

// NewMIDIDriver creates a new MIDI driver.
func NewMIDIDriver(cat *CATClient, config *MIDIConfig) *MIDIDriver {
	return &MIDIDriver{
		cat:    cat,
		config: config,
		index:  buildMappingIndex(config),
		done:   make(chan struct{}),
	}
}

// ListMIDIDevices prints available MIDI input devices to stdout.
func ListMIDIDevices() error {
	midiIn, err := rtmidi.NewMIDIInDefault()
	if err != nil {
		return fmt.Errorf("MIDI init: %w", err)
	}
	defer midiIn.Destroy()

	count, err := midiIn.PortCount()
	if err != nil {
		return fmt.Errorf("MIDI port count: %w", err)
	}
	if count == 0 {
		fmt.Println("No MIDI input devices found.")
		return nil
	}
	fmt.Printf("MIDI input devices (%d):\n", count)
	for i := 0; i < count; i++ {
		name, err := midiIn.PortName(i)
		if err != nil {
			continue
		}
		fmt.Printf("  %d: %s\n", i, name)
	}
	return nil
}

// Run opens the MIDI device and starts listening. Blocks until Close is called.
func (d *MIDIDriver) Run() error {
	midiIn, err := rtmidi.NewMIDIInDefault()
	if err != nil {
		return fmt.Errorf("MIDI init: %w", err)
	}
	d.midiIn = midiIn

	count, err := midiIn.PortCount()
	if err != nil {
		return fmt.Errorf("MIDI port count: %w", err)
	}

	portIdx := -1
	for i := 0; i < count; i++ {
		name, err := midiIn.PortName(i)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(name), strings.ToLower(d.config.Device)) {
			portIdx = i
			log.Printf("MIDI: matched device %q at port %d", name, i)
			break
		}
	}
	if portIdx < 0 {
		return fmt.Errorf("MIDI device %q not found", d.config.Device)
	}

	if err := midiIn.SetCallback(d.handleMessage); err != nil {
		return fmt.Errorf("MIDI set callback: %w", err)
	}

	midiIn.IgnoreTypes(true, true, true)

	if err := midiIn.OpenPort(portIdx, "wk4"); err != nil {
		return fmt.Errorf("MIDI open port: %w", err)
	}

	log.Printf("MIDI: listening on port %d", portIdx)
	<-d.done
	return nil
}

// Close shuts down the MIDI driver.
func (d *MIDIDriver) Close() {
	if d.midiIn != nil {
		d.midiIn.Close()
		d.midiIn.Destroy()
	}
	close(d.done)
}

// handleMessage is the rtmidi callback for incoming MIDI messages.
func (d *MIDIDriver) handleMessage(midiIn rtmidi.MIDIIn, msg []byte, deltaTime float64) {
	if len(msg) < 2 {
		return
	}
	status := msg[0]
	msgType := status & 0xF0
	channel := status & 0x0F

	switch msgType {
	case 0x90: // Note On
		if len(msg) < 3 {
			return
		}
		number := msg[1]
		velocity := msg[2]
		if velocity == 0 {
			d.handleNoteOff(channel, number)
			return
		}
		key := midiKey{msgType: 0x90, channel: channel, number: number}
		m, ok := d.index[key]
		if !ok {
			return
		}
		log.Printf("MIDI: note_on ch=%d note=%d vel=%d -> %s", channel, number, velocity, m.Action)
		d.dispatch(m, int(velocity))

	case 0x80: // Note Off
		if len(msg) < 3 {
			return
		}
		d.handleNoteOff(channel, msg[1])

	case 0xB0: // Control Change
		if len(msg) < 3 {
			return
		}
		number := msg[1]
		value := msg[2]
		key := midiKey{msgType: 0xB0, channel: channel, number: number}
		m, ok := d.index[key]
		if !ok {
			return
		}
		log.Printf("MIDI: cc ch=%d cc=%d val=%d -> %s", channel, number, value, m.Action)
		d.dispatch(m, int(value))
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
		go d.tuneVFO(value, step)

	default:
		log.Printf("MIDI: unknown action %q", m.Action)
	}
}

// tuneVFO adjusts VFO A frequency relative to current.
func (d *MIDIDriver) tuneVFO(value, step int) {
	delta := (value - 64) * step
	if delta == 0 {
		return
	}
	hz, err := d.cat.GetFreqA()
	if err != nil {
		log.Printf("MIDI: GetFreqA failed: %v", err)
		return
	}
	hz += delta
	if hz < 0 {
		hz = 0
	}
	d.cat.SetFreqA(hz)
}
