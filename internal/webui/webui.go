package webui

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
	"strings"
	"sync"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
	"github.com/ujay/barncat-sparks/internal/config"
	"github.com/ujay/barncat-sparks/internal/keyer"
	"github.com/ujay/barncat-sparks/internal/midi"
)

//go:embed webui.html
var webuiHTML embed.FS

// WebUI holds the HTTP server and runtime state.
type WebUI struct {
	mu        sync.Mutex
	config    *config.AppConfig
	cfgPath   string
	running   bool
	statusMsg string
	cat       cat.Radio
	wk        *keyer.Winkeyer
	midiDrv   *midi.MIDIDriver
	catServer   *cat.CATServer
	radioServer *cat.RadioServer
	server    *http.Server

	// Decode panel state (guarded by decodeMu, separate from main mu)
	decodeMu      sync.Mutex
	decodeBuf     strings.Builder
	decodeEnabled bool
	decodeCancel  context.CancelFunc
}

// RunWebUI starts the web UI on a free localhost port and opens the browser.
func RunWebUI(configPath string) {
	cfg, err := config.LoadAppConfig(configPath)
	if err != nil {
		cfg = config.DefaultAppConfig()
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("sparks %s: WinKeyer emulator & MIDI controller for K4", config.Version)

	w := &WebUI{
		config:    cfg,
		cfgPath:   configPath,
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
	mux.HandleFunc("/api/decode", w.handleDecode)
	mux.HandleFunc("/api/version", w.handleVersion)
	mux.HandleFunc("/api/about", w.handleAbout)

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

	waitForGUI(url, "BarnCat Sparks "+config.Version, 760, 900) // blocks until exit

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
		var cfg config.AppConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		w.mu.Lock()
		w.config = &cfg
		w.mu.Unlock()
		if err := config.SaveAppConfig(w.cfgPath, &cfg); err != nil {
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
	Running      bool   `json:"running"`
	Message      string `json:"message"`
	RTTYMode     bool   `json:"rtty_mode"`
	DecodeActive bool   `json:"decode_active"`
}

func (w *WebUI) handleStatus(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	resp := statusResponse{Running: w.running, Message: w.statusMsg}
	if w.wk != nil {
		resp.RTTYMode = w.wk.IsRTTY()
	}
	w.mu.Unlock()
	w.decodeMu.Lock()
	resp.DecodeActive = w.decodeEnabled
	w.decodeMu.Unlock()
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
	if err := config.SaveAppConfig(w.cfgPath, w.config); err != nil {
		w.statusMsg = fmt.Sprintf("Save failed: %v", err)
		writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
		return
	}

	// Connect CAT (TCP or serial)
	client := cat.NewCATClient()
	switch w.config.CATTransport {
	case "serial":
		w.statusMsg = "Connecting to radio via serial..."
		if err := client.ConnectSerial(w.config.CATSerialPort, w.config.CATSerialBaud); err != nil {
			w.statusMsg = fmt.Sprintf("CAT serial error: %v", err)
			writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
			return
		}
	default:
		w.statusMsg = "Connecting to radio via TCP..."
		if err := client.ConnectTCP(w.config.CATAddress); err != nil {
			w.statusMsg = fmt.Sprintf("CAT TCP error: %v", err)
			writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
			return
		}
	}
	w.cat = client

	// Start CATServer if listen address configured
	if w.config.CATListen != "" {
		srv, err := cat.NewCATServer(client, w.config.CATListen)
		if err != nil {
			w.statusMsg = fmt.Sprintf("CATServer error: %v", err)
			// Non-fatal — continue without server
		} else {
			w.catServer = srv
			go srv.Run()
		}
	}

	// Start RadioServer if listen address configured
	if w.config.RadioServerListen != "" {
		rsrv, err := cat.NewRadioServer(w.cat, w.config.RadioServerListen)
		if err != nil {
			w.statusMsg = fmt.Sprintf("RadioServer error: %v", err)
			// Non-fatal — continue without server
		} else {
			w.radioServer = rsrv
			go rsrv.Run()
		}
	}

	// Start WinKeyer if serial port selected
	if w.config.SerialPort != "" {
		w.wk = keyer.NewWinkeyer(w.cat)
		w.wk.SetWPM(w.config.KeyerSpeed)
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
		w.midiDrv = midi.NewMIDIDriver(w.cat, midiCfg)
		go func() {
			if err := w.midiDrv.Run(); err != nil {
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
	if w.config.RadioServerListen != "" {
		status += fmt.Sprintf(", RadioServer: %s", w.config.RadioServerListen)
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
	// Stop decode goroutine and disable radio's text decoder
	w.decodeMu.Lock()
	wasDecoding := w.decodeEnabled
	if w.decodeCancel != nil {
		w.decodeCancel()
		w.decodeCancel = nil
	}
	w.decodeEnabled = false
	w.decodeBuf.Reset()
	w.decodeMu.Unlock()
	if wasDecoding && w.cat != nil {
		w.cat.SetTextDecode(false)
	}

	if w.midiDrv != nil {
		w.midiDrv.Close()
		w.midiDrv = nil
	}
	if w.wk != nil {
		w.wk.Close()
		w.wk = nil
	}
	if w.radioServer != nil {
		w.radioServer.Close()
		w.radioServer = nil
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
	ports := config.ListSerialPorts()
	if ports == nil {
		ports = []string{}
	}
	writeJSON(rw, ports)
}

func (w *WebUI) handleMIDIDevices(rw http.ResponseWriter, r *http.Request) {
	devices := midi.EnumerateMIDIDevices()
	if devices == nil {
		devices = []string{}
	}
	writeJSON(rw, devices)
}

// actionInfo is the JSON shape for a single action in /api/actions.
type actionInfo struct {
	Name    string       `json:"name"`
	Label   string       `json:"label"`
	Options []optionInfo `json:"options,omitempty"`
}

type optionInfo struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Default string `json:"default"`
}

func (w *WebUI) handleActions(rw http.ResponseWriter, r *http.Request) {
	names := config.ActionNames()
	actions := make([]actionInfo, len(names))
	for i, name := range names {
		def := config.ActionRegistry[name]
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
	writeJSON(rw, map[string]string{"version": config.Version})
}

func (w *WebUI) handleAbout(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, config.GetAboutInfo())
}

// handleDecode handles GET (poll decoded text) and POST (enable/disable) for the decode panel.
func (w *WebUI) handleDecode(rw http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.decodeMu.Lock()
		text := w.decodeBuf.String()
		w.decodeBuf.Reset()
		enabled := w.decodeEnabled
		w.decodeMu.Unlock()
		writeJSON(rw, map[string]any{"enabled": enabled, "text": text})

	case http.MethodPost:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		w.mu.Lock()
		running := w.running
		c := w.cat
		w.mu.Unlock()

		if !running || c == nil {
			http.Error(rw, "not running", http.StatusBadRequest)
			return
		}

		w.decodeMu.Lock()
		if req.Enabled && !w.decodeEnabled {
			c.SetTextDecode(true)
			ctx, cancel := context.WithCancel(context.Background())
			w.decodeCancel = cancel
			w.decodeEnabled = true
			go w.decodeLoop(ctx, c)
		} else if !req.Enabled && w.decodeEnabled {
			if w.decodeCancel != nil {
				w.decodeCancel()
				w.decodeCancel = nil
			}
			w.decodeEnabled = false
			c.SetTextDecode(false)
		}
		w.decodeMu.Unlock()
		writeJSON(rw, map[string]bool{"enabled": req.Enabled})

	default:
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// decodeLoop polls the K4 text decoder every 250ms and accumulates text.
func (w *WebUI) decodeLoop(ctx context.Context, c cat.Radio) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			text, err := c.QueryDecode()
			if err != nil {
				log.Printf("decode: %v", err)
				continue
			}
			if text != "" {
				w.decodeMu.Lock()
				w.decodeBuf.WriteString(text)
				w.decodeMu.Unlock()
			}
		}
	}
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(v)
}
