# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run

```bash
# Build (no CGO required)
go build -o wk4 .

# Cross-compile for Windows (GUI, no console window)
GOOS=windows go build -ldflags "-H windowsgui" -o wk4.exe .

# Tests
go test ./...                              # Run tests
go vet ./...                               # Lint
```

**No CGO required.** The GUI is a web UI served by Go's `net/http` (embedded HTML via `go:embed`). MIDI uses Windows `winmm.dll` syscalls directly (build-tagged in `midi_windows.go`). On non-Windows platforms, MIDI functions compile as stubs. There is no Makefile; use standard `go build` commands.

### Running Modes

- **Web UI (default):** `./wk4` — starts an HTTP server on localhost and opens the browser. Configure serial port, CAT address, MIDI device, and mappings. Settings saved to `wk4-config.json`.
- **Headless (CLI):** `./wk4 -port COM5 -midi DJ2GO2` — backwards-compatible CLI mode, activated when `-port`, `-midi`, or `-nogui` flags are present.

## Architecture

wk4 is a **WinKeyer3 emulator and MIDI controller for the Elecraft K4** radio. It bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands, and optionally connects a MIDI controller for hands-on radio control.

### Data Flow

```
N1MM Logger ↔ virtual serial port ↔ Winkeyer (winkeyer.go) ↔ CATClient (catclient.go) ↔ K4 radio (TCP or serial)
MIDI controller (USB) ↔ MIDIDriver (midi_windows.go) ─────↗
Browser ↔ WebUI (webui.go, webui.html) ─── config/control ──↗
N1MM Logger ↔ TCP ↔ CATServer (catserver.go) ──→ CATClient ──→ serial ──→ K3/K4 radio
```

### Key Components

- **`main.go`** — Entry point. Routes between web UI mode (default, no CLI flags) and headless mode (when `-port`, `-midi`, or `-nogui` flags are present). Backwards compatible with the original CLI interface.

- **`webui.go`** — HTTP server and REST API. Serves the embedded `webui.html` on localhost, handles config load/save, start/stop of all subsystems, serial port and MIDI device enumeration. Auto-opens browser on startup.

- **`webui.html`** — Single-page web UI embedded via `go:embed`. Dark-themed HTML with inline CSS and JS. Connection (serial port, CAT address, keyer speed), MIDI (device, mapping table), Options, Start/Stop, and status bar sections. Communicates with the Go backend via JSON REST API.

- **`config.go`** — `AppConfig` struct that holds all wk4 settings (serial port, CAT address/transport/serial/baud/listen, keyer speed, MIDI device, mappings). Serializes to/from `wk4-config.json`. Includes `ToMIDIConfig()` to convert to the existing `MIDIConfig` format, and `listSerialPorts()` for serial port enumeration.

- **`actions.go`** — Action registry (`ActionRegistry`) mapping action names to human-readable labels and configurable options. Used by the web UI to populate dropdowns and option fields. `ActionNames()` returns sorted names for UI display.

- **`winkeyer.go`** — Core protocol engine. Implements a byte-level state machine (`stIdle → stAdmin/stParam → stAdminParam`) that parses the WinKeyer binary protocol. Manages a 64-byte CW character buffer with XON/XOFF flow control. The `sendLoop` goroutine drains the buffer in word-sized chunks (split on spaces), sends KY commands to the radio, and echoes characters back at the correct WPM timing. Each `sendLoop` instance receives its own `done`/`cancel` channels for clean abort support.

- **`catclient.go`** — Transport-agnostic CAT client. Connects to radios via TCP (`ConnectTCP`) or serial (`ConnectSerial`, using `io.ReadWriteCloser`). Sends Elecraft CAT commands (`KY` for CW text, `KS` for speed, `TX`/`RX` for PTT, `FA`/`FB` for VFO frequency, `AG` for volume, `BU`/`BD` for band, `FT`/`SWT` for VFO select). Reads responses split on semicolons. Supports response relay (`AddRelay`/`RemoveRelay`) for CATServer to broadcast to multiple TCP clients.

- **`catserver.go`** — TCP relay server. Accepts multiple TCP clients and relays CAT commands to/from the CATClient's transport (serial or TCP). Enables N1MM or a remote wk4 instance to connect to a serial radio over the network. Responses are broadcast to all connected clients.

- **`midi.go`** — Cross-platform MIDI config types, mapping index, and dispatch logic. Loads a JSON config file (`midi-config.json`) that maps MIDI events (CC, Note On/Off) to K4 actions (VFO tune, volume, speed, PTT, band up/down, VFO A/B/swap). O(1) mapping lookup via `midiKey`.

- **`midi_windows.go`** — Windows MIDI driver using `winmm.dll` syscalls (`midiInOpen`, `midiInStart`, etc.). Callback sends raw MIDI events to a buffered channel; a goroutine dispatches them via `handleMIDIEvent`.

- **`midi_other.go`** — Stub MIDI driver for non-Windows platforms. `ListMIDIDevices` prints "not supported", `Run()` returns an error. Includes cross-platform `midiEvent` type and `mimData` constant for compile compatibility.

- **`midi_list_windows.go`** / **`midi_list_other.go`** — Platform-specific MIDI device enumeration. Windows uses `winmm.dll` to list devices; non-Windows returns an empty list.

- **`morse.go`** — Lookup table mapping ASCII characters to their duration in dit units. Used by `charDuration()` to pace character echo timing based on WPM (PARIS standard: 1 dit = 1200/WPM ms).

### Concurrency Model

The `Winkeyer` struct uses two mutexes: `mu` guards protocol state and the CW buffer; `writeMu` guards serial port writes. Each `sendLoop` goroutine receives its own `done` (completion) and `cancel` (abort) channels — `clearBuffer` closes `cancel` to stop the active sendLoop without racing on shared channel state. The `MIDIDriver` event loop processes MIDI events synchronously (one at a time); VFO tuning caches the frequency locally and throttles CAT sends to ~20/sec. The `CATClient.send()` method is mutex-protected, allowing concurrent calls from both subsystems. Lock order is always `Winkeyer.mu` → `CATClient.mu` to prevent deadlocks.

### Serial Port Configuration

WinKeyer protocol uses 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits). Reports as WinKeyer version 31 (WK3.1).
