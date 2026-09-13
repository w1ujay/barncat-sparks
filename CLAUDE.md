# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run

```bash
# Build (no CGO required)
go build -o barncat-sparks ./cmd/barncat-sparks/

# Cross-compile for Windows (GUI, no console window)
GOOS=windows go build -ldflags "-H windowsgui" -o barncat-sparks.exe ./cmd/barncat-sparks/

# Tests
go test ./...                              # Run tests
go vet ./...                               # Lint
```

**No CGO required.** The GUI is a web UI served by Go's `net/http` (embedded HTML via `go:embed`). MIDI uses Windows `winmm.dll` syscalls directly (build-tagged in `midi_windows.go`). On non-Windows platforms, MIDI functions compile as stubs. There is no Makefile; use standard `go build` commands.

### Running Modes

- **Web UI (default):** `./barncat-sparks` — starts an HTTP server on localhost and opens the browser. Configure serial port, CAT address, MIDI device, and mappings. Settings saved to `sparks-config.json`.
- **Headless (CLI):** `./barncat-sparks -port COM5 -midi DJ2GO2` — backwards-compatible CLI mode, activated when `-port`, `-midi`, or `-nogui` flags are present.

## Project Layout

```
cmd/barncat-sparks/          # Entry point (package main)
  main.go                    # CLI flags, headless mode, launches web UI
  winres/                    # Windows icon/manifest resources (go-winres)
internal/
  cat/                       # CAT protocol — transport-agnostic radio client & TCP relay
    catclient.go             # CATClient, Connect{TCP,Serial}, CAT commands
    catserver.go             # CATServer (TCP relay for N1MM, remote clients)
  keyer/                     # WinKeyer protocol engine
    winkeyer.go              # Byte-level state machine, CW buffer, sendLoop
    morse.go                 # Morse timing lookup (dit units per char)
  midi/                      # MIDI controller support
    midi.go                  # MIDIConfig, MIDIMapping, dispatch logic
    midi_windows.go          # MIDIDriver (Windows winmm.dll)
    midi_other.go            # MIDIDriver (stub for non-Windows)
    midi_list_windows.go     # EnumerateMIDIDevices (Windows)
    midi_list_other.go       # EnumerateMIDIDevices (stub)
  config/                    # Configuration & metadata
    config.go                # AppConfig, load/save, ListSerialPorts
    actions.go               # ActionRegistry, ActionDef
    about.go                 # Version, contributors
  webui/                     # Web UI (HTTP server + embedded HTML)
    webui.go                 # HTTP handlers, REST API
    webui.html               # Single-page web UI (go:embed)
    gui_windows.go           # waitForGUI (WebView2)
    gui_other.go             # waitForGUI (app-mode browser)
    browser.go               # findBrowser, launchAppMode (!windows)
```

## Architecture

BarnCat Sparks is a **WinKeyer3 emulator and MIDI controller for the Elecraft K4** radio. It bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands, and optionally connects a MIDI controller for hands-on radio control.

### Data Flow

```
N1MM Logger ↔ virtual serial port ↔ Winkeyer (keyer/) ↔ CATClient (cat/) ↔ K4 radio (TCP or serial)
MIDI controller (USB) ↔ MIDIDriver (midi/) ─────────────↗
Browser ↔ WebUI (webui/) ─── config/control ─────────────↗
N1MM Logger ↔ TCP ↔ CATServer (cat/) ──→ CATClient ──→ serial ──→ K3/K4 radio
```

### Dependency Graph (acyclic)

```
cmd/barncat-sparks  →  webui, cat, keyer, midi, config
webui               →  cat, keyer, midi, config
config              →  midi  (for MIDIConfig/MIDIMapping in ToMIDIConfig)
keyer               →  cat
midi                →  cat
cat                 →  (stdlib only)
```

### Key Components

- **`cmd/barncat-sparks/main.go`** — Entry point. Routes between web UI mode (default, no CLI flags) and headless mode (when `-port`, `-midi`, or `-nogui` flags are present). Backwards compatible with the original CLI interface.

- **`internal/webui/`** — HTTP server and REST API. Serves the embedded `webui.html` on localhost, handles config load/save, start/stop of all subsystems, serial port and MIDI device enumeration. Auto-opens browser on startup. Includes platform-specific GUI integration (WebView2 on Windows, app-mode Chromium on Linux/macOS).

- **`internal/cat/`** — Transport-agnostic CAT client. Connects to radios via TCP (`ConnectTCP`) or serial (`ConnectSerial`). Sends Elecraft CAT commands. Reads responses split on semicolons. CATServer accepts multiple TCP clients and relays commands to/from the radio.

- **`internal/keyer/`** — Core WinKeyer protocol engine. Implements a byte-level state machine that parses the WinKeyer binary protocol. Manages a 64-byte CW character buffer with XON/XOFF flow control. Includes Morse timing lookup for character echo pacing.

- **`internal/midi/`** — Cross-platform MIDI config types, mapping index, and dispatch logic. Maps MIDI events (CC, Note On/Off) to K4 actions. Windows driver uses `winmm.dll` syscalls; non-Windows builds use stubs.

- **`internal/config/`** — `AppConfig` struct, load/save to `sparks-config.json`, serial port enumeration, action registry for MIDI mappings, and version/about info.

### Concurrency Model

The `Winkeyer` struct uses two mutexes: `mu` guards protocol state and the CW buffer; `writeMu` guards serial port writes. Each `sendLoop` goroutine receives its own `done` (completion) and `cancel` (abort) channels — `clearBuffer` closes `cancel` to stop the active sendLoop without racing on shared channel state. The `MIDIDriver` event loop processes MIDI events synchronously (one at a time); VFO tuning caches the frequency locally and throttles CAT sends to ~20/sec. The `CATClient.Send()` method is mutex-protected, allowing concurrent calls from both subsystems. Query methods (`GetFreqA`, `GetFreqB`, `QuerySpeed`, `QueryMode`) share one response channel, so each holds `CATClient.queryMu` for its whole drain → send → await transaction. Lock order is always `Winkeyer.mu` → `CATClient.queryMu` → `CATClient.mu` to prevent deadlocks. `Winkeyer.Close()` releases anything still keying the radio (aborts busy CW, drops RTTY/tune PTT) before the CAT link is closed.

### Serial Port Configuration

WinKeyer protocol uses 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits). Reports as WinKeyer version 31 (WK3.1).
