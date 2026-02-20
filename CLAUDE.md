# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run

```bash
CGO_ENABLED=1 go build -o wk4 .              # Linux (CGO required for MIDI)
CGO_ENABLED=1 GOOS=windows go build -o wk4.exe .  # Cross-compile for Windows
go test ./...                   # Run tests
go vet ./...                    # Lint
```

CGO is required because the MIDI driver uses go-rtmidi (wraps the RtMidi C library). On Linux, `libasound2-dev` must be installed for ALSA headers. There is no Makefile; use standard `go build` commands.

## Architecture

wk4 is a **WinKeyer3 emulator and MIDI controller for the Elecraft K4** radio. It bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands, and optionally connects a MIDI controller for hands-on radio control.

### Data Flow

```
N1MM Logger ↔ virtual serial port ↔ Winkeyer (winkeyer.go) ↔ CATClient (catclient.go) ↔ K4 radio (TCP)
MIDI controller (USB) ↔ MIDIDriver (midi.go) ─────────────↗
```

### Key Components

- **`winkeyer.go`** — Core protocol engine. Implements a byte-level state machine (`stIdle → stAdmin/stParam → stAdminParam`) that parses the WinKeyer binary protocol. Manages a 64-byte CW character buffer with XON/XOFF flow control. The `sendLoop` goroutine drains the buffer in chunks (max 24 chars), sends KY commands to the radio, and echoes characters back at the correct WPM timing.

- **`catclient.go`** — TCP client that sends Elecraft CAT commands (`KY` for CW text, `KS` for speed, `TX`/`RX` for PTT, `FA`/`FB` for VFO frequency, `AG` for volume, `BU`/`BD` for band, `FT`/`SWT` for VFO select). Reads responses split on semicolons. Connection is persistent with a background `readLoop`.

- **`midi.go`** — MIDI controller driver using go-rtmidi. Loads a JSON config file (`midi-config.json`) that maps MIDI events (CC, Note On/Off) to K4 actions (VFO tune, volume, speed, PTT, band up/down, VFO A/B/swap). Uses a callback-based event loop with O(1) mapping lookup.

- **`morse.go`** — Lookup table mapping ASCII characters to their duration in dit units. Used by `charDuration()` to pace character echo timing based on WPM (PARIS standard: 1 dit = 1200/WPM ms).

### Concurrency Model

The `Winkeyer` struct uses two mutexes: `mu` guards protocol state and the CW buffer; `writeMu` guards serial port writes. The `MIDIDriver` uses `mu` to guard PTT toggle state. Both dispatch CAT commands via `go` to avoid blocking their respective read/callback loops. The `CATClient.send()` method is mutex-protected, allowing concurrent calls from both subsystems.

### Serial Port Configuration

WinKeyer protocol uses 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits). Reports as WinKeyer version 31 (WK3.1).
