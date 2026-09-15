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
	"github.com/ujay/barncat-sparks/internal/controls"
	"github.com/ujay/barncat-sparks/internal/keyer"
	"github.com/ujay/barncat-sparks/internal/lp100a"
	"github.com/ujay/barncat-sparks/internal/midi"
)

//go:embed webui.html
var webuiHTML embed.FS

// WebUI holds the HTTP server and runtime state.
type WebUI struct {
	mu           sync.Mutex
	config       *config.AppConfig
	cfgPath      string
	running      bool
	statusMsg    string
	cat          cat.Radio
	qk4VolumeCat *cat.CATClient     // optional QK4 connection for volume routing
	exec         *controls.Executor // runs actions for MIDI (and the knob)
	knob         knobRunner         // the USB knob reader while Started
	knobNote     string             // status text after the knob's live state: a start failure and ignored rows
	knobLearnMu  sync.Mutex         // one Learn at a time
	wk           *keyer.Winkeyer
	midiDrv      *midi.MIDIDriver
	catServer    *cat.CATServer
	radioServer  *cat.RadioServer
	lp100        *lp100a.Driver
	server       *http.Server

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
	mux.HandleFunc("/api/control-names", w.handleControlNames)
	mux.HandleFunc("/api/knob/status", w.handleKnobStatus)
	mux.HandleFunc("/api/knob/learn", w.handleKnobLearn)
	mux.HandleFunc("/api/trace", w.handleTrace)
	mux.HandleFunc("/api/decode", w.handleDecode)
	mux.HandleFunc("/api/version", w.handleVersion)
	mux.HandleFunc("/api/about", w.handleAbout)
	mux.HandleFunc("/api/hearth/status", w.handleHearthStatus)
	mux.HandleFunc("/api/hearth/test", w.handleHearthTest)
	mux.HandleFunc("/api/hearth/k4", w.handleHearthK4)
	mux.HandleFunc("/api/hearth/powersave", w.handleHearthPowerSave)

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
	resp := statusResponse{Running: w.running, Message: w.statusMsg + w.knobStatusLocked()}
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
		if w.config.CATPassword != "" {
			w.statusMsg = "Connecting to radio via K4 auth..."
			if err := client.ConnectK4(w.config.CATAddress, w.config.CATPassword); err != nil {
				w.statusMsg = fmt.Sprintf("K4 auth error: %v", err)
				writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
				return
			}
		} else {
			w.statusMsg = "Connecting to radio via TCP..."
			if err := client.ConnectTCP(w.config.CATAddress); err != nil {
				w.statusMsg = fmt.Sprintf("CAT TCP error: %v", err)
				writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
				return
			}
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

	// Start RadioServer if listen address configured.
	// Run() is deferred until after all Handle() calls (amp proxy, LP-100A, etc.).
	if w.config.RadioServerListen != "" {
		rsrv, err := cat.NewRadioServer(w.cat, w.config.RadioServerListen)
		if err != nil {
			w.statusMsg = fmt.Sprintf("RadioServer error: %v", err)
			// Non-fatal — continue without server
		} else {
			if w.config.AmpURL != "" {
				if err := rsrv.SetAmpProxy(w.config.AmpURL); err != nil {
					log.Printf("RadioServer: amp proxy error: %v", err)
				}
			}
			w.radioServer = rsrv
		}
	}

	// Start LP-100A if port configured
	if w.config.LP100Port != "" {
		lp := lp100a.NewDriver(w.config.LP100Port)
		if err := lp.Connect(); err != nil {
			log.Printf("LP-100A: %v", err)
			// Non-fatal — continue without wattmeter
		} else {
			lp.StartPolling()
			w.lp100 = lp
			if w.radioServer != nil {
				h := lp.HTTPHandler()
				w.radioServer.Handle("/api/lp100a", h)
				w.radioServer.Handle("/api/lp100a/", h)
			}
		}
	}

	// Start RadioServer now that all HTTP handlers are registered
	if w.radioServer != nil {
		go w.radioServer.Run()
	}

	// Start WinKeyer if serial port selected
	if w.config.SerialPort != "" {
		w.wk = keyer.NewWinkeyer(w.cat)
		w.wk.SetWPM(w.config.KeyerSpeed)
		if err := w.wk.OpenSerial(w.config.SerialPort); err != nil {
			// Fatal: roll back everything started above (servers, wattmeter,
			// CAT) so a retry can bind the same listen addresses.
			msg := fmt.Sprintf("Serial error: %v", err)
			w.wk = nil
			w.stopLocked()
			w.statusMsg = msg
			writeJSON(rw, statusResponse{Running: false, Message: w.statusMsg})
			return
		}
		w.cat.SetSpeed(w.config.KeyerSpeed)
		go w.wk.Run()
		if w.config.ProtocolTrace {
			w.wk.SetTrace(true)
		}
	}

	// Start MIDI and the knob; both run actions on one executor.
	midiOn := len(w.config.GetMIDIDevices()) > 0
	if midiOn || w.config.KnobEnabled {
		w.exec = controls.New(w.cat, w.config.PaddleSwap)

		// Route volume commands to QK4 if enabled
		if w.config.QK4Volume {
			addr := w.config.QK4Address
			if addr == "" {
				addr = "127.0.0.1:9299"
			}
			qk4 := cat.NewCATClient()
			if err := qk4.ConnectTCP(addr); err != nil {
				log.Printf("QK4 volume route: %v (volume will use main radio)", err)
			} else {
				w.qk4VolumeCat = qk4
				w.exec.SetVolumeTarget(qk4)
				log.Printf("QK4 volume route: connected to %s", addr)
			}
		}
	}
	if midiOn {
		w.midiDrv = midi.NewMIDIDriver(w.exec, w.config.ToMIDIConfig())
		go func() {
			if err := w.midiDrv.Run(); err != nil {
				w.mu.Lock()
				w.statusMsg = fmt.Sprintf("MIDI: %v", err)
				w.mu.Unlock()
			}
		}()
	}
	if w.config.KnobEnabled {
		w.startKnobLocked()
	}

	w.running = true
	var status string
	switch w.config.CATTransport {
	case "serial":
		status = fmt.Sprintf("Running — Radio: %s (%d baud)", w.config.CATSerialPort, w.config.CATSerialBaud)
	default:
		if w.config.CATPassword != "" {
			status = fmt.Sprintf("Running — Radio: %s (K4 auth)", w.config.CATAddress)
		} else {
			status = fmt.Sprintf("Running — Radio: %s (TCP)", w.config.CATAddress)
		}
	}
	if w.config.CATListen != "" {
		status += fmt.Sprintf(", Server: %s", w.config.CATListen)
	}
	if w.config.RadioServerListen != "" {
		status += fmt.Sprintf(", RadioServer: %s", w.config.RadioServerListen)
		if w.config.AmpURL != "" {
			status += fmt.Sprintf(" (amp: %s)", w.config.AmpURL)
		}
	}
	if w.lp100 != nil {
		status += fmt.Sprintf(", LP-100A: %s", w.config.LP100Port)
	}
	if w.config.SerialPort != "" {
		status += fmt.Sprintf(", WinKeyer: %s", w.config.SerialPort)
	}
	if devs := w.config.GetMIDIDevices(); len(devs) > 0 {
		status += fmt.Sprintf(", MIDI: %s", strings.Join(devs, ", "))
	}
	w.statusMsg = status

	writeJSON(rw, statusResponse{Running: true, Message: w.statusMsg + w.knobStatusLocked()})
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

	if w.knob != nil {
		w.knob.Close()
		w.knob = nil
	}
	w.knobNote = ""
	if w.midiDrv != nil {
		w.midiDrv.Close()
		w.midiDrv = nil
	}
	// After the inputs, before the QK4 link: releases a held PTT.
	if w.exec != nil {
		w.exec.Close()
		w.exec = nil
	}
	if w.qk4VolumeCat != nil {
		w.qk4VolumeCat.Close()
		w.qk4VolumeCat = nil
	}
	if w.wk != nil {
		w.wk.Close()
		w.wk = nil
	}
	if w.lp100 != nil {
		w.lp100.StopPolling()
		w.lp100.Disconnect()
		w.lp100 = nil
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
	Name      string       `json:"name"`
	Label     string       `json:"label"`
	Kind      string       `json:"kind"`
	Momentary bool         `json:"momentary"`
	Options   []optionInfo `json:"options,omitempty"`
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
		a := actionInfo{Name: name, Label: def.Label, Kind: def.Kind, Momentary: def.Momentary}
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

func (w *WebUI) handleControlNames(rw http.ResponseWriter, r *http.Request) {
	names := make(map[string]config.ControlMap, len(config.ControlMaps)+1)
	for device, cmap := range config.ControlMaps {
		names[device] = cmap
	}
	names["knob"] = config.KnobControlNames
	writeJSON(rw, names)
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
	writeJSONCode(rw, http.StatusOK, v)
}
