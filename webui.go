package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

//go:embed webui.html
var webuiHTML embed.FS

// WebUI holds the HTTP server and runtime state.
type WebUI struct {
	mu        sync.Mutex
	config    *AppConfig
	cfgPath   string
	version   string
	running   bool
	statusMsg string
	cat       *CATClient
	wk        *Winkeyer
	midi      *MIDIDriver
	catServer *CATServer
	server    *http.Server
}

// RunWebUI starts the web UI on a free localhost port and opens the browser.
func RunWebUI(configPath, version string) {
	cfg, err := LoadAppConfig(configPath)
	if err != nil {
		cfg = DefaultAppConfig()
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("wk4 %s: WinKeyer emulator & MIDI controller for K4", version)

	w := &WebUI{
		config:    cfg,
		cfgPath:   configPath,
		version:   version,
		statusMsg: "Stopped",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", w.handleIndex)
	mux.HandleFunc("/api/config", w.handleConfig)
	mux.HandleFunc("/api/status", w.handleStatus)
	mux.HandleFunc("/api/start", w.handleStart)
	mux.HandleFunc("/api/stop", w.handleStop)
	mux.HandleFunc("/api/serial-ports", w.handleSerialPorts)
	mux.HandleFunc("/api/midi-devices", w.handleMIDIDevices)
	mux.HandleFunc("/api/actions", w.handleActions)
	mux.HandleFunc("/api/trace", w.handleTrace)
	mux.HandleFunc("/api/version", w.handleVersion)

	// Find a free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("webui: failed to listen: %v", err)
	}
	addr := listener.Addr().String()
	url := "http://" + addr

	w.server = &http.Server{Handler: mux}

	log.Printf("webui: listening on %s", url)

	// Start HTTP server in a goroutine
	serverDone := make(chan struct{})
	go func() {
		if err := w.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("webui: server error: %v", err)
		}
		close(serverDone)
	}()

	waitForGUI(url, "wk4 "+version, 760, 900) // blocks until exit

	w.Shutdown()
	<-serverDone
}

func openBrowser(url string) {
	// Small delay to let the server start
	time.Sleep(200 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}

// handleIndex serves the embedded HTML page.
func (w *WebUI) handleIndex(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}
	data, _ := webuiHTML.ReadFile("webui.html")
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Write(data)
}

// handleConfig handles GET (load) and POST (save) for AppConfig.
func (w *WebUI) handleConfig(rw http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.mu.Lock()
		cfg := w.config
		w.mu.Unlock()
		writeJSON(rw, cfg)
	case http.MethodPost:
		var cfg AppConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		w.mu.Lock()
		w.config = &cfg
		w.mu.Unlock()
		if err := SaveAppConfig(w.cfgPath, &cfg); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, map[string]string{"status": "saved"})
	default:
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// statusResponse is the JSON shape for /api/status.
type statusResponse struct {
	Running bool   `json:"running"`
	Message string `json:"message"`
}

func (w *WebUI) handleStatus(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	resp := statusResponse{Running: w.running, Message: w.statusMsg}
	w.mu.Unlock()
	writeJSON(rw, resp)
}

func (w *WebUI) handleStart(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		writeJSON(rw, statusResponse{Running: true, Message: w.statusMsg})
		return
	}

	// Save config before starting
	if err := SaveAppConfig(w.cfgPath, w.config); err != nil {
		w.statusMsg = fmt.Sprintf("Save failed: %v", err)
		writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
		return
	}

	// Connect CAT (TCP or serial)
	w.cat = NewCATClient()
	switch w.config.CATTransport {
	case "serial":
		w.statusMsg = "Connecting to radio via serial..."
		if err := w.cat.ConnectSerial(w.config.CATSerialPort, w.config.CATSerialBaud); err != nil {
			w.statusMsg = fmt.Sprintf("CAT serial error: %v", err)
			w.cat = nil
			writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
			return
		}
	default:
		w.statusMsg = "Connecting to radio via TCP..."
		if err := w.cat.ConnectTCP(w.config.CATAddress); err != nil {
			w.statusMsg = fmt.Sprintf("CAT TCP error: %v", err)
			w.cat = nil
			writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
			return
		}
	}

	// Start CATServer if listen address configured
	if w.config.CATListen != "" {
		srv, err := NewCATServer(w.cat, w.config.CATListen)
		if err != nil {
			w.statusMsg = fmt.Sprintf("CATServer error: %v", err)
			// Non-fatal — continue without server
		} else {
			w.catServer = srv
			go srv.Run()
		}
	}

	// Start WinKeyer if serial port selected
	if w.config.SerialPort != "" {
		w.wk = NewWinkeyer(w.cat)
		w.wk.wpm = w.config.KeyerSpeed
		if err := w.wk.OpenSerial(w.config.SerialPort); err != nil {
			w.statusMsg = fmt.Sprintf("Serial error: %v", err)
			w.cat.Close()
			w.cat = nil
			w.wk = nil
			writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
			return
		}
		w.cat.SetSpeed(w.config.KeyerSpeed)
		go w.wk.Run()
		if w.config.ProtocolTrace {
			w.wk.SetTrace(true)
		}
	}

	// Start MIDI if device selected
	if w.config.MIDIDevice != "" {
		midiCfg := w.config.ToMIDIConfig()
		w.midi = NewMIDIDriver(w.cat, midiCfg)
		go func() {
			if err := w.midi.Run(); err != nil {
				w.mu.Lock()
				w.statusMsg = fmt.Sprintf("MIDI: %v", err)
				w.mu.Unlock()
			}
		}()
	}

	w.running = true
	var status string
	switch w.config.CATTransport {
	case "serial":
		status = fmt.Sprintf("Running — Radio: %s (%d baud)", w.config.CATSerialPort, w.config.CATSerialBaud)
	default:
		status = fmt.Sprintf("Running — Radio: %s (TCP)", w.config.CATAddress)
	}
	if w.config.CATListen != "" {
		status += fmt.Sprintf(", Server: %s", w.config.CATListen)
	}
	if w.config.SerialPort != "" {
		status += fmt.Sprintf(", WinKeyer: %s", w.config.SerialPort)
	}
	if w.config.MIDIDevice != "" {
		status += fmt.Sprintf(", MIDI: %s", w.config.MIDIDevice)
	}
	w.statusMsg = status

	writeJSON(rw, statusResponse{Running: true, Message: w.statusMsg})
}

func (w *WebUI) handleStop(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.stopLocked()
	writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
}

// stopLocked shuts down all subsystems. Must be called with w.mu held.
func (w *WebUI) stopLocked() {
	if w.midi != nil {
		w.midi.Close()
		w.midi = nil
	}
	if w.wk != nil {
		w.wk.Close()
		w.wk = nil
	}
	if w.catServer != nil {
		w.catServer.Close()
		w.catServer = nil
	}
	if w.cat != nil {
		w.cat.Close()
		w.cat = nil
	}
	w.running = false
	w.statusMsg = "Stopped"
}

// Shutdown gracefully stops the web UI server and all subsystems.
func (w *WebUI) Shutdown() {
	w.mu.Lock()
	w.stopLocked()
	w.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w.server.Shutdown(ctx)
}

func (w *WebUI) handleSerialPorts(rw http.ResponseWriter, r *http.Request) {
	ports := listSerialPorts()
	if ports == nil {
		ports = []string{}
	}
	writeJSON(rw, ports)
}

func (w *WebUI) handleMIDIDevices(rw http.ResponseWriter, r *http.Request) {
	devices := enumerateMIDIDevices()
	if devices == nil {
		devices = []string{}
	}
	writeJSON(rw, devices)
}

// actionInfo is the JSON shape for a single action in /api/actions.
type actionInfo struct {
	Name    string      `json:"name"`
	Label   string      `json:"label"`
	Options []optionInfo `json:"options,omitempty"`
}

type optionInfo struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Default string `json:"default"`
}

func (w *WebUI) handleActions(rw http.ResponseWriter, r *http.Request) {
	names := ActionNames()
	actions := make([]actionInfo, len(names))
	for i, name := range names {
		def := ActionRegistry[name]
		a := actionInfo{Name: name, Label: def.Label}
		for _, opt := range def.Options {
			a.Options = append(a.Options, optionInfo{
				Key:     opt.Key,
				Label:   opt.Label,
				Default: opt.Default,
			})
		}
		actions[i] = a
	}
	writeJSON(rw, actions)
}

func (w *WebUI) handleTrace(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.running || w.wk == nil {
		http.Error(rw, "not running", http.StatusBadRequest)
		return
	}

	w.wk.SetTrace(req.Enabled)
	writeJSON(rw, map[string]bool{"enabled": req.Enabled})
}

func (w *WebUI) handleVersion(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, map[string]string{"version": w.version})
}

func writeJSON(rw http.ResponseWriter, v interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(v)
}
