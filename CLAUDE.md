# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run

```bash
go build -o wk4 .                          # Linux (WinKeyer only, MIDI stubs)
GOOS=windows go build -o wk4.exe .         # Windows (WinKeyer + MIDI)
go test ./...                              # Run tests
go vet ./...                               # Lint
```

No CGO required. MIDI uses Windows `winmm.dll` syscalls directly (build-tagged in `midi_windows.go`). On non-Windows platforms, MIDI functions compile as stubs. There is no Makefile; use standard `go build` commands.

## Architecture

wk4 is a **WinKeyer3 emulator and MIDI controller for the Elecraft K4** radio. It bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands, and optionally connects a MIDI controller for hands-on radio control.

### Data Flow

```
N1MM Logger ↔ virtual serial port ↔ Winkeyer (winkeyer.go) ↔ CATClient (catclient.go) ↔ K4 radio (TCP)
MIDI controller (USB) ↔ MIDIDriver (midi_windows.go) ─────↗
```

### Key Components

- **`winkeyer.go`** — Core protocol engine. Implements a byte-level state machine (`stIdle → stAdmin/stParam → stAdminParam`) that parses the WinKeyer binary protocol. Manages a 64-byte CW character buffer with XON/XOFF flow control. The `sendLoop` goroutine drains the buffer in word-sized chunks (split on spaces), sends KY commands to the radio, and echoes characters back at the correct WPM timing. Each `sendLoop` instance receives its own `done`/`cancel` channels for clean abort support.

- **`catclient.go`** — TCP client that sends Elecraft CAT commands (`KY` for CW text, `KS` for speed, `TX`/`RX` for PTT, `FA`/`FB` for VFO frequency, `AG` for volume, `BU`/`BD` for band, `FT`/`SWT` for VFO select). Reads responses split on semicolons. Connection is persistent with a background `readLoop`.

- **`midi.go`** — Cross-platform MIDI config types, mapping index, and dispatch logic. Loads a JSON config file (`midi-config.json`) that maps MIDI events (CC, Note On/Off) to K4 actions (VFO tune, volume, speed, PTT, band up/down, VFO A/B/swap). O(1) mapping lookup via `midiKey`.

- **`midi_windows.go`** — Windows MIDI driver using `winmm.dll` syscalls (`midiInOpen`, `midiInStart`, etc.). Callback sends raw MIDI events to a buffered channel; a goroutine dispatches them via `handleMIDIEvent`.

- **`midi_other.go`** — Stub MIDI driver for non-Windows platforms. `ListMIDIDevices` prints "not supported", `Run()` returns an error.

- **`morse.go`** — Lookup table mapping ASCII characters to their duration in dit units. Used by `charDuration()` to pace character echo timing based on WPM (PARIS standard: 1 dit = 1200/WPM ms).

### Concurrency Model

The `Winkeyer` struct uses two mutexes: `mu` guards protocol state and the CW buffer; `writeMu` guards serial port writes. Each `sendLoop` goroutine receives its own `done` (completion) and `cancel` (abort) channels — `clearBuffer` closes `cancel` to stop the active sendLoop without racing on shared channel state. The `MIDIDriver` event loop processes MIDI events synchronously (one at a time); VFO tuning caches the frequency locally and throttles CAT sends to ~20/sec. The `CATClient.send()` method is mutex-protected, allowing concurrent calls from both subsystems. Lock order is always `Winkeyer.mu` → `CATClient.mu` to prevent deadlocks.

### Serial Port Configuration

WinKeyer protocol uses 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits). Reports as WinKeyer version 31 (WK3.1).
