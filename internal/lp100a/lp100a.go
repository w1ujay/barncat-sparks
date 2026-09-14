package lp100a

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// Data holds a single LP-100A measurement sample.
type Data struct {
	Power        float64 `json:"power"`
	Impedance    float64 `json:"impedance"`
	Phase        float64 `json:"phase"`
	PowerDBm     float64 `json:"power_dbm"`
	SWR          float64 `json:"swr"`
	PeakSWR      float64 `json:"peak_swr"`
	SWRAlarmPoint int    `json:"swr_alarm_point"`
	PowerRange   int     `json:"power_range"`
	PeakHoldMode int     `json:"peak_hold_mode"`
	Callsign     string  `json:"callsign"`
	Timestamp    time.Time `json:"timestamp"`
}

// State is the JSON-serializable snapshot of the driver.
type State struct {
	PortName   string    `json:"port_name"`
	Connected  bool      `json:"connected"`
	LastUpdate time.Time `json:"last_update"`
	Data       Data      `json:"data"`
	Error      string    `json:"error,omitempty"`
}

// Driver manages a serial connection to an LP-100A wattmeter.
type Driver struct {
	mu       sync.RWMutex
	portName string
	port     serial.Port
	state    State
	stop     chan struct{}

	// Peak SWR tracking
	peakSWR       float64
	peakSWRTime   time.Time
}

const peakSWRTimeout = 4 * time.Second

// NewDriver creates a new LP-100A driver for the given serial port.
func NewDriver(portName string) *Driver {
	return &Driver{
		portName: portName,
		state:    State{PortName: portName},
	}
}

// Connect opens the serial port at 115200 baud.
func (d *Driver) Connect() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	port, err := serial.Open(d.portName, &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	})
	if err != nil {
		d.state.Error = fmt.Sprintf("open %s: %v", d.portName, err)
		return fmt.Errorf("lp100a: %s", d.state.Error)
	}
	port.SetReadTimeout(500 * time.Millisecond)
	d.port = port
	d.state.Connected = true
	d.state.Error = ""
	log.Printf("lp100a: connected to %s", d.portName)
	return nil
}

// Disconnect closes the serial port and clears state.
func (d *Driver) Disconnect() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.disconnectLocked()
}

func (d *Driver) disconnectLocked() {
	if d.port != nil {
		d.port.Close()
		d.port = nil
	}
	d.state.Connected = false
	d.state.Data = Data{}
	d.state.Error = ""
	d.peakSWR = 0
	d.peakSWRTime = time.Time{}
	log.Printf("lp100a: disconnected")
}

// StartPolling begins a 200ms poll loop in a goroutine.
func (d *Driver) StartPolling() {
	d.mu.Lock()
	d.stop = make(chan struct{})
	d.mu.Unlock()
	go d.pollLoop()
}

// StopPolling stops the poll loop.
func (d *Driver) StopPolling() {
	d.mu.Lock()
	if d.stop != nil {
		close(d.stop)
		d.stop = nil
	}
	d.mu.Unlock()
}

func (d *Driver) pollLoop() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	d.mu.RLock()
	stop := d.stop
	d.mu.RUnlock()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			d.poll()
		}
	}
}

func (d *Driver) poll() {
	d.mu.Lock()
	port := d.port
	d.mu.Unlock()

	if port == nil {
		return
	}

	// Send 'P' command
	if _, err := port.Write([]byte{'P'}); err != nil {
		d.mu.Lock()
		d.state.Error = fmt.Sprintf("write: %v", err)
		d.mu.Unlock()
		return
	}

	time.Sleep(50 * time.Millisecond)

	buf := make([]byte, 256)
	n, err := port.Read(buf)
	if err != nil {
		d.mu.Lock()
		d.state.Error = fmt.Sprintf("read: %v", err)
		d.mu.Unlock()
		return
	}

	raw := string(buf[:n])
	data, err := parseResponse(raw)
	if err != nil {
		d.mu.Lock()
		d.state.Error = fmt.Sprintf("parse: %v", err)
		d.mu.Unlock()
		return
	}

	d.mu.Lock()
	data.Timestamp = time.Now()

	// Track peak SWR
	if data.SWR > d.peakSWR {
		d.peakSWR = data.SWR
		d.peakSWRTime = data.Timestamp
	}
	// Expire peak after timeout
	if !d.peakSWRTime.IsZero() && data.Timestamp.Sub(d.peakSWRTime) > peakSWRTimeout {
		d.peakSWR = 0
		d.peakSWRTime = time.Time{}
	}
	data.PeakSWR = d.peakSWR

	d.state.Data = data
	d.state.LastUpdate = data.Timestamp
	d.state.Error = ""
	d.mu.Unlock()
}

// SendCommand sends a single command byte to the LP-100A.
func (d *Driver) SendCommand(cmd byte) error {
	d.mu.RLock()
	port := d.port
	d.mu.RUnlock()

	if port == nil {
		return fmt.Errorf("lp100a: not connected")
	}
	_, err := port.Write([]byte{cmd})
	return err
}

// ResetPeakSWR clears the tracked peak SWR value.
func (d *Driver) ResetPeakSWR() {
	d.mu.Lock()
	d.peakSWR = 0
	d.peakSWRTime = time.Time{}
	d.state.Data.PeakSWR = 0
	d.mu.Unlock()
}

// GetState returns a snapshot of the current driver state.
func (d *Driver) GetState() State {
	d.mu.RLock()
	s := d.state
	// Lazy expire peak SWR on read
	if !d.peakSWRTime.IsZero() && time.Since(d.peakSWRTime) > peakSWRTimeout {
		s.Data.PeakSWR = 0
	}
	d.mu.RUnlock()
	return s
}

// parseResponse parses an LP-100A CSV response into a Data struct.
// Format: ;power,impedance,phase,dBm,SWR,alarm,range,peakMode,callsign
func parseResponse(raw string) (Data, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimLeft(raw, ";")
	if raw == "" {
		return Data{}, fmt.Errorf("empty response")
	}

	fields := strings.Split(raw, ",")
	if len(fields) < 9 {
		return Data{}, fmt.Errorf("expected 9 fields, got %d", len(fields))
	}

	var d Data
	var err error

	d.Power, err = strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
	if err != nil {
		return Data{}, fmt.Errorf("power: %w", err)
	}
	d.Impedance, err = strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
	if err != nil {
		return Data{}, fmt.Errorf("impedance: %w", err)
	}
	d.Phase, err = strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
	if err != nil {
		return Data{}, fmt.Errorf("phase: %w", err)
	}
	d.PowerDBm, err = strconv.ParseFloat(strings.TrimSpace(fields[3]), 64)
	if err != nil {
		return Data{}, fmt.Errorf("dBm: %w", err)
	}
	d.SWR, err = strconv.ParseFloat(strings.TrimSpace(fields[4]), 64)
	if err != nil {
		return Data{}, fmt.Errorf("swr: %w", err)
	}

	alm, err := strconv.Atoi(strings.TrimSpace(fields[5]))
	if err != nil {
		return Data{}, fmt.Errorf("alarm: %w", err)
	}
	d.SWRAlarmPoint = alm

	rng, err := strconv.Atoi(strings.TrimSpace(fields[6]))
	if err != nil {
		return Data{}, fmt.Errorf("range: %w", err)
	}
	d.PowerRange = rng

	phm, err := strconv.Atoi(strings.TrimSpace(fields[7]))
	if err != nil {
		return Data{}, fmt.Errorf("peakMode: %w", err)
	}
	d.PeakHoldMode = phm

	d.Callsign = strings.TrimSpace(fields[8])

	return d, nil
}

// HTTPHandler returns an http.Handler serving the LP-100A REST API.
//
//	GET  /api/lp100a          → State JSON
//	POST /api/lp100a/command  → {"command":"..."}
func (d *Driver) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/lp100a", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.GetState())
	})
	mux.HandleFunc("/api/lp100a/command", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Command string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}{}

		switch req.Command {
		case "connect":
			if err := d.Connect(); err != nil {
				resp.Message = err.Error()
			} else {
				d.StartPolling()
				resp.Success = true
				resp.Message = "connected"
			}
		case "disconnect":
			d.StopPolling()
			d.Disconnect()
			resp.Success = true
			resp.Message = "disconnected"
		case "reset_peak":
			d.ResetPeakSWR()
			resp.Success = true
			resp.Message = "peak SWR reset"
		case "A", "M", "F", "P":
			if err := d.SendCommand(req.Command[0]); err != nil {
				resp.Message = err.Error()
			} else {
				resp.Success = true
				resp.Message = fmt.Sprintf("sent command %s", req.Command)
			}
		default:
			resp.Message = fmt.Sprintf("unknown command: %s", req.Command)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	return mux
}
