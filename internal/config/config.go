package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ujay/barncat-sparks/internal/midi"
	"go.bug.st/serial"
)

// AppConfig holds all BarnCat Sparks settings, serialized to sparks-config.json.
type AppConfig struct {
	SerialPort    string         `json:"serial_port"`
	CATAddress    string         `json:"cat_address"`
	CATTransport  string         `json:"cat_transport"`
	CATSerialPort string         `json:"cat_serial_port"`
	CATSerialBaud int            `json:"cat_serial_baud"`
	CATListen          string         `json:"cat_listen"`
	RadioServerListen  string         `json:"radio_server_listen"`
	AmpURL             string         `json:"amp_url"`
	LP100Port          string         `json:"lp100_port"`
	KeyerSpeed    int            `json:"keyer_speed"`
	MIDIDevice    string         `json:"midi_device"`
	MinimizeToTray bool           `json:"minimize_to_tray"`
	ProtocolTrace  bool           `json:"protocol_trace"`
	Mappings       []MappingEntry `json:"mappings"`
}

// MappingEntry defines a single MIDI-to-action mapping in the config file.
type MappingEntry struct {
	Action  string                 `json:"action"`
	Type    string                 `json:"type"`
	Channel int                    `json:"channel"`
	Number  int                    `json:"number"`
	Opts    map[string]any `json:"opts,omitempty"`
}

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

// SaveAppConfig writes an AppConfig to a JSON file.
func SaveAppConfig(path string, cfg *AppConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// ToMIDIConfig converts AppConfig mappings to the MIDIConfig format
// used by the existing MIDI driver.
func (c *AppConfig) ToMIDIConfig() *midi.MIDIConfig {
	mc := &midi.MIDIConfig{
		Device:   c.MIDIDevice,
		Mappings: make([]midi.MIDIMapping, len(c.Mappings)),
	}
	for i, m := range c.Mappings {
		mc.Mappings[i] = midi.MIDIMapping{
			Type:    m.Type,
			Channel: m.Channel,
			Number:  m.Number,
			Action:  m.Action,
			Opts:    m.Opts,
		}
	}
	return mc
}

// ListSerialPorts returns available serial port names.
func ListSerialPorts() []string {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil
	}
	return ports
}
