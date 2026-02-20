# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run

```bash
# Linux (requires gcc for Fyne/CGO)
CGO_ENABLED=1 go build -o wk4 .

# Windows (requires MinGW-w64 in PATH for Fyne/CGO)
CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows go build -o wk4.exe .

# Tests (unchanged)
go test ./...                              # Run tests
go vet ./...                               # Lint
```

**CGO is required** since the Fyne GUI toolkit depends on C libraries. A C compiler (gcc on Linux, MinGW-w64 on Windows cross-compile) must be available. MIDI uses Windows `winmm.dll` syscalls directly (build-tagged in `midi_windows.go`). On non-Windows platforms, MIDI functions compile as stubs. There is no Makefile; use standard `go build` commands.

### Running Modes

- **GUI (default):** `./wk4` — launches a Fyne window for configuring serial port, CAT address, MIDI device, and mappings. Settings are saved to `wk4-config.json`.
- **Headless (CLI):** `./wk4 -port COM5 -midi DJ2GO2` — backwards-compatible CLI mode, activated when `-port`, `-midi`, or `-nogui` flags are present.

## Architecture

wk4 is a **WinKeyer3 emulator and MIDI controller for the Elecraft K4** radio. It bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands, and optionally connects a MIDI controller for hands-on radio control.

### Data Flow

```
N1MM Logger ↔ virtual serial port ↔ Winkeyer (winkeyer.go) ↔ CATClient (catclient.go) ↔ K4 radio (TCP)
MIDI controller (USB) ↔ MIDIDriver (midi_windows.go) ─────↗
```

### Key Components

- **`main.go`** — Entry point. Routes between GUI mode (default, no CLI flags) and headless mode (when `-port`, `-midi`, or `-nogui` flags are present). Backwards compatible with the original CLI interface.

- **`gui.go`** — Fyne-based GUI application. Dark-themed window with Connection (serial port, CAT address, keyer speed), MIDI (device, mapping table with Learn button), and Options (minimize-to-tray) sections. Start/Stop buttons connect and disconnect all subsystems. System tray support for background operation.

- **`config.go`** — `AppConfig` struct that holds all wk4 settings (serial port, CAT address, keyer speed, MIDI device, mappings, tray option). Serializes to/from `wk4-config.json`. Includes `ToMIDIConfig()` to convert to the existing `MIDIConfig` format, and `listSerialPorts()` for serial port enumeration.

- **`actions.go`** — Action registry (`ActionRegistry`) mapping action names to human-readable labels and configurable options. Used by the GUI to populate dropdowns and option fields. `ActionNames()` returns sorted names for UI display.

- **`winkeyer.go`** — Core protocol engine. Implements a byte-level state machine (`stIdle → stAdmin/stParam → stAdminParam`) that parses the WinKeyer binary protocol. Manages a 64-byte CW character buffer with XON/XOFF flow control. The `sendLoop` goroutine drains the buffer in word-sized chunks (split on spaces), sends KY commands to the radio, and echoes characters back at the correct WPM timing. Each `sendLoop` instance receives its own `done`/`cancel` channels for clean abort support.

- **`catclient.go`** — TCP client that sends Elecraft CAT commands (`KY` for CW text, `KS` for speed, `TX`/`RX` for PTT, `FA`/`FB` for VFO frequency, `AG` for volume, `BU`/`BD` for band, `FT`/`SWT` for VFO select). Reads responses split on semicolons. Connection is persistent with a background `readLoop`.

- **`midi.go`** — Cross-platform MIDI config types, mapping index, and dispatch logic. Loads a JSON config file (`midi-config.json`) that maps MIDI events (CC, Note On/Off) to K4 actions (VFO tune, volume, speed, PTT, band up/down, VFO A/B/swap). O(1) mapping lookup via `midiKey`.

- **`midi_windows.go`** — Windows MIDI driver using `winmm.dll` syscalls (`midiInOpen`, `midiInStart`, etc.). Callback sends raw MIDI events to a buffered channel; a goroutine dispatches them via `handleMIDIEvent`.

- **`midi_other.go`** — Stub MIDI driver for non-Windows platforms. `ListMIDIDevices` prints "not supported", `Run()` returns an error. Includes cross-platform `midiEvent` type and `mimData` constant for GUI compile compatibility.

- **`gui_midi.go`** / **`gui_midi_windows.go`** / **`gui_midi_other.go`** — Platform-specific MIDI device enumeration for the GUI dropdown. Windows uses `winmm.dll` to list devices; non-Windows returns an empty list.

- **`morse.go`** — Lookup table mapping ASCII characters to their duration in dit units. Used by `charDuration()` to pace character echo timing based on WPM (PARIS standard: 1 dit = 1200/WPM ms).

### Concurrency Model

The `Winkeyer` struct uses two mutexes: `mu` guards protocol state and the CW buffer; `writeMu` guards serial port writes. Each `sendLoop` goroutine receives its own `done` (completion) and `cancel` (abort) channels — `clearBuffer` closes `cancel` to stop the active sendLoop without racing on shared channel state. The `MIDIDriver` event loop processes MIDI events synchronously (one at a time); VFO tuning caches the frequency locally and throttles CAT sends to ~20/sec. The `CATClient.send()` method is mutex-protected, allowing concurrent calls from both subsystems. Lock order is always `Winkeyer.mu` → `CATClient.mu` to prevent deadlocks.

### Serial Port Configuration

WinKeyer protocol uses 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits). Reports as WinKeyer version 31 (WK3.1).
