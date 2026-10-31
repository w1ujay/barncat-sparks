package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ujay/barncat-sparks/internal/hidknob"
	"github.com/ujay/barncat-sparks/internal/midi"
	"go.bug.st/serial"
)

// AppConfig holds all BarnCat Sparks settings, serialized to sparks-config.json.
type AppConfig struct {
	SerialPort        string         `json:"serial_port"`
	CATAddress        string         `json:"cat_address"`
	CATTransport      string         `json:"cat_transport"`
	CATSerialPort     string         `json:"cat_serial_port"`
	CATSerialBaud     int            `json:"cat_serial_baud"`
	CATPassword       string         `json:"cat_password,omitempty"`
	CATListen         string         `json:"cat_listen"`
	RadioServerListen string         `json:"radio_server_listen"`
	RadioModel        string         `json:"radio_model,omitempty"` // "k4" (default) or "k3"; headless only
	AmpURL            string         `json:"amp_url"`
	LP100Port         string         `json:"lp100_port"`
	KeyerSpeed        int            `json:"keyer_speed"`
	MIDIDevice        string         `json:"midi_device,omitempty"`
	MIDIDevices       []string       `json:"midi_devices,omitempty"`
	QK4Volume         bool           `json:"qk4_volume,omitempty"`
	QK4Address        string         `json:"qk4_address,omitempty"`
	HearthURL         string         `json:"hearth_url"`
	HearthReplicaURL  string         `json:"hearth_replica_url,omitempty"`
	HearthToken       string         `json:"hearth_token,omitempty"`
	HearthSite        string         `json:"hearth_site,omitempty"`
	MinimizeToTray    bool           `json:"minimize_to_tray"`
	ProtocolTrace     bool           `json:"protocol_trace"`
	KnobEnabled       bool           `json:"knob_enabled"`
	UpdatePrereleases bool           `json:"update_prereleases,omitempty"`
	Mappings          []MappingEntry `json:"mappings"`
}

// MappingEntry defines a single mapping in the config file: a MIDI control
// (type "cc" or "note_on") or a knob gesture (type "knob", channel = HID report
// ID, number = usage code) and the action it runs.
type MappingEntry struct {
	Action  string         `json:"action"`
	Type    string         `json:"type"`
	Channel int            `json:"channel"`
	Number  int            `json:"number"`
	Opts    map[string]any `json:"opts,omitempty"`
}

const (
	// MappingTypeKnob marks a mapping row for the USB knob.
	MappingTypeKnob = "knob"
	// KnobReportID is the knob's consumer-control report, stored as the row's channel.
	KnobReportID = 3
)

// DefaultAppConfig returns a config with sensible defaults.
func DefaultAppConfig() *AppConfig {
	return &AppConfig{
		CATAddress:    "localhost:9299",
		CATTransport:  "tcp",
		CATSerialBaud: 38400,
		KeyerSpeed:    25,
	}
}

// LoadAppConfig reads an AppConfig from a JSON file.
func LoadAppConfig(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// SaveAppConfig writes an AppConfig to a JSON file, backing up the file it
// replaces.
func SaveAppConfig(path string, cfg *AppConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return writeConfig(path, data)
}

// GetMIDIDevices returns the configured MIDI device names, merging legacy
// single-device and multi-device fields for backward compatibility.
func (c *AppConfig) GetMIDIDevices() []string {
	if len(c.MIDIDevices) > 0 {
		return c.MIDIDevices
	}
	if c.MIDIDevice != "" {
		return []string{c.MIDIDevice}
	}
	return nil
}

// ToMIDIConfig converts AppConfig mappings to the MIDIConfig format used by the
// MIDI driver. Knob rows are left out.
func (c *AppConfig) ToMIDIConfig() *midi.MIDIConfig {
	mc := &midi.MIDIConfig{
		Devices: c.GetMIDIDevices(),
	}
	for _, m := range c.Mappings {
		if m.Type == MappingTypeKnob {
			continue
		}
		mc.Mappings = append(mc.Mappings, midi.MIDIMapping{
			Type:    m.Type,
			Channel: m.Channel,
			Number:  m.Number,
			Action:  m.Action,
			Opts:    m.Opts,
		})
	}
	return mc
}

// ToKnobMappings converts Knob rows to knob mappings. A row the knob can't use
// is skipped with a reason naming its 1-based position in the mappings list,
// the same numbering the Settings page shows.
func (c *AppConfig) ToKnobMappings() ([]hidknob.Mapping, []string) {
	var maps []hidknob.Mapping
	var skipped []string
	for i, m := range c.Mappings {
		if m.Type != MappingTypeKnob {
			continue
		}
		row := i + 1
		def, known := ActionRegistry[m.Action]
		switch {
		case m.Action == "":
			skipped = append(skipped, fmt.Sprintf("row %d: no action chosen", row))
			continue
		case !known:
			skipped = append(skipped, fmt.Sprintf("row %d: unknown action %q", row, m.Action))
			continue
		case m.Number <= 0 || m.Number > 0x3FF:
			skipped = append(skipped, fmt.Sprintf("row %d: knob gesture not learned yet", row))
			continue
		case def.Momentary:
			skipped = append(skipped, fmt.Sprintf("row %d: %s can't be used on the knob", row, def.Label))
			continue
		case def.Kind == KindAbsolute:
			skipped = append(skipped, fmt.Sprintf("row %d: %s needs a slider, not the knob", row, def.Label))
			continue
		}
		direction := 0
		if def.Kind == KindRelative {
			direction = knobDirection(m.Opts)
			if direction == 0 {
				skipped = append(skipped, fmt.Sprintf("row %d: %s needs a direction (+ or −)", row, def.Label))
				continue
			}
		}
		maps = append(maps, hidknob.Mapping{
			Usage:     uint16(m.Number),
			Action:    m.Action,
			Opts:      m.Opts,
			Direction: direction,
		})
	}
	return maps, skipped
}

// knobDirection reads opts.direction: 1 or -1, or 0 when missing or invalid.
// JSON numbers decode as float64.
func knobDirection(opts map[string]any) int {
	switch v := opts["direction"].(type) {
	case float64:
		if v == 1 || v == -1 {
			return int(v)
		}
	case int:
		if v == 1 || v == -1 {
			return v
		}
	}
	return 0
}

// ListSerialPorts returns available serial port names.
func ListSerialPorts() []string {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil
	}
	return ports
}
