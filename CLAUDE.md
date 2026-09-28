# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run

```bash
# Build (no CGO required)
go build -o barncat-sparks ./cmd/barncat-sparks/

# Cross-compile for Windows (GUI, no console window)
GOOS=windows go build -ldflags "-H windowsgui" -o barncat-sparks.exe ./cmd/barncat-sparks/

# Raspberry Pi (dragonroad K3 server); CGO_ENABLED=0 is required on WSL
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o barncat-sparks-arm64 ./cmd/barncat-sparks/

# Tests
go test ./...                              # Run tests
go vet ./...                               # Lint
```

**No CGO required.** The GUI is a web UI served by Go's `net/http` (embedded HTML via `go:embed`). MIDI uses Windows `winmm.dll` syscalls directly (build-tagged in `midi_windows.go`). On non-Windows platforms, MIDI functions compile as stubs. There is no Makefile; use standard `go build` commands.

### Running Modes

- **Web UI (default):** `./barncat-sparks` — starts an HTTP server on localhost and opens the browser. Configure serial port, CAT address, MIDI device, and mappings. Settings saved to `sparks-config.json`.
- **Headless (CLI):** `./barncat-sparks -port COM5 -midi DJ2GO2` — backwards-compatible CLI mode, activated when `-port`, `-midi`, or `-nogui` flags are present.
- **Remote K3 server:** `./barncat-sparks -nogui -radio-model k3 -cat-transport serial -cat-serial <port> -radio-listen <tailnet-ip>:9400` — see docs/pi-setup.md.

## Project Layout

```
cmd/barncat-sparks/          # Entry point (package main)
  main.go                    # CLI flags, headless mode, launches web UI
  winres/                    # Windows icon/manifest resources (go-winres)
deploy/barncat-sparks.service  # systemd unit for the dragonroad Pi
internal/
  cat/                       # CAT protocol — transport-agnostic radio client & TCP relay
    catclient.go             # CATClient, Connect{TCP,Serial}, CAT commands
    catserver.go             # CATServer (TCP relay for N1MM, remote clients)
    radio.go                 # Radio interface (incl. Raw passthrough)
    k3radio.go               # K3Radio: K4 dialect → K3, Raw policy table
    kysender.go              # paced KY text sender (abort-atomic)
    toggler.go               # NR/notch reconciler (reads DS, taps SWT)
  keyer/                     # WinKeyer protocol engine
    winkeyer.go              # Byte-level state machine, CW buffer, sendLoop
    morse.go                 # Morse timing lookup (dit units per char)
  controls/                  # Shared action executor for hands-on inputs
    executor.go              # Event, Executor: action dispatch, VFO cache/throttle, PTT
  midi/                      # MIDI controller support
    midi.go                  # MIDIConfig, MIDIMapping, MIDI message → controls.Event
    midi_windows.go          # MIDIDriver (Windows winmm.dll)
    midi_other.go            # MIDIDriver (stub for non-Windows)
    midi_list_windows.go     # EnumerateMIDIDevices (Windows)
    midi_list_other.go       # EnumerateMIDIDevices (stub)
  config/                    # Configuration & metadata
    config.go                # AppConfig, load/save, ListSerialPorts
    actions.go               # ActionRegistry, ActionDef
    about.go                 # Version, contributors
  hearth/                    # lempster-hearth control client (stdlib only)
    client.go                # Client, errors, Pi→replica fallback, WhoAmI
    status.go                # Status (K4 + power-save), replica merge
    control.go               # K4Power, SetPowerSave
    wol.go                   # WolTargets, Wake (Pi-only wake-on-LAN)
  hidknob/                   # DROK/iWit USB knob input over WinUSB
    hidknob.go               # Mapping, State, errors, DecodeReport, event translation
    knob.go                  # Knob reader loop, Learn, Probe, LearnOnce
    transport_windows.go     # WinUSB open/read (setupapi, winusb.dll)
    transport_other.go       # stub: unsupported
  webui/                     # Web UI (HTTP server + embedded HTML)
    webui.go                 # HTTP handlers, REST API
    hearth.go                # /api/hearth/* — hearth status + K4/power-save/WoL controls
    knob.go                  # /api/knob/* — knob status and Learn; knob start in Start
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
MIDI controller (USB) ↔ MIDIDriver (midi/) ──→ Executor (controls/) ──↗
USB knob (WinUSB) ↔ Knob (hidknob/) ──────────→ Executor (controls/) ──↗
Browser ↔ WebUI (webui/) ─── config/control ─────────────────────────↗
N1MM Logger ↔ TCP ↔ CATServer (cat/) ──→ CATClient ──→ serial ──→ K3/K4 radio
```

### Dependency Graph (acyclic)

```
cmd/barncat-sparks  →  webui, cat, keyer, midi, controls, config
webui               →  cat, keyer, midi, controls, hidknob, config, hearth
config              →  midi, hidknob  (ToMIDIConfig, ToKnobMappings)
keyer               →  cat
midi                →  controls
controls            →  cat
hidknob             →  controls
cat                 →  (stdlib only)
hearth              →  (stdlib only)
```

### Key Components

- **`cmd/barncat-sparks/main.go`** — Entry point. Routes between web UI mode (default, no CLI flags) and headless mode (when `-port`, `-midi`, or `-nogui` flags are present). Backwards compatible with the original CLI interface.

- **`internal/webui/`** — HTTP server and REST API. Serves the embedded `webui.html` on localhost, handles config load/save, start/stop of all subsystems, serial port and MIDI device enumeration. Auto-opens browser on startup. Includes platform-specific GUI integration (WebView2 on Windows, app-mode Chromium on Linux/macOS).

- **`internal/cat/`** — Transport-agnostic CAT client. Connects to radios via TCP (`ConnectTCP`) or serial (`ConnectSerial`). Sends Elecraft CAT commands. Reads responses split on semicolons. CATServer accepts multiple TCP clients and relays commands to/from the radio.

- **`internal/keyer/`** — Core WinKeyer protocol engine. Implements a byte-level state machine that parses the WinKeyer binary protocol. Manages a 64-byte ring buffer of CW characters (logical positions, slots reused once sendLoop takes them) with XON/XOFF flow control based on unsent characters. Includes Morse timing lookup for character echo pacing.

- **`internal/controls/`** — The action executor shared by MIDI and the knob. Runs mapped actions (VFO tuning with a frequency cache and throttle, gains, filters, toggles, PTT) against the radio one event at a time.

- **`internal/midi/`** — Cross-platform MIDI config types, mapping index, and dispatch logic. Maps MIDI events (CC, Note On/Off) to `controls.Event`s for the shared executor. Windows driver uses `winmm.dll` syscalls; non-Windows builds use stubs.

- **`internal/hidknob/`** — The USB knob as an input. Reads the knob's HID reports over WinUSB (after a one-time Zadig driver swap), decodes consumer-control report 3, and hands mapped usages to the `controls.Executor`. Mappings come from `type: "knob"` rows; Learn captures the next usage for the Settings page.

- **`internal/config/`** — `AppConfig` struct, load/save to `sparks-config.json`, serial port enumeration, action registry for MIDI mappings, and version/about info.

### Concurrency Model

The `Winkeyer` struct uses three mutexes: `mu` guards protocol state and the CW buffer; `writeMu` guards serial port writes; `sendMu` makes sendLoop's cancel check and its `KY` one step, and the abort paths (`Close`, clear buffer) take it before `SendAbort`, so a `KY` reaches the radio before the abort or not at all. Each `sendLoop` goroutine receives its own `done` (completion) and `cancel` (abort) channels — `clearBuffer` closes `cancel` to stop the active sendLoop without racing on shared channel state. Hands-on inputs send `controls.Event`s to one `controls.Executor`; `Handle` is synchronous and holds `Executor.mu` for the whole event, so events from all inputs run one at a time in arrival order. VFO tuning caches the frequency and throttles CAT sends to ~20/sec; a step the throttle swallows arms a one-shot timer that sends the cached frequency when the throttle lapses (under `Executor.mu`, skipped after `Close`), so the last steps of a fast spin always reach the radio. Every CAT command, PTT included, is sent synchronously under `Executor.mu`, so commands reach the radio in event order; `Executor.Close` marks it closed and releases a held PTT, and no PTT is keyed after that. `CATClient.Send()` serializes writes under `CATClient.writeMu`, allowing concurrent calls from both subsystems; `CATClient.mu` guards only the transport pointer and is never held across I/O, so `Close` closes a transport whose write is stuck, and a TCP write gives up after 2 s (`catWriteTimeout`). Each CATServer relay has its own bounded queue and writer goroutine with a write deadline (`relay.go`), so a client that stops reading is disconnected instead of stalling `readLoop`. Query methods (`GetFreqA`, `GetFreqB`, `QuerySpeed`, `QueryMode`) share one response channel, so each holds `CATClient.queryMu` for its whole drain → send → await transaction. Lock order is always `Winkeyer.mu` → `CATClient.queryMu` → `CATClient.writeMu` to prevent deadlocks. Every WinKeyer stop path — `Close`, clear buffer, reset, host close — goes through `stopKeyingLocked` + `sendStop`: it aborts CW that may still be keying and drops PTT held by an RTTY bracket, a pending `]` release, a tune carrier, or buffered PTT (0x18), sending under `sendMu`. A serial read failure before `Close` takes the same path, since the logger's release can no longer arrive. `Close` does this before the CAT link is closed. Tune (key immediate) and buffered (0x18) PTT are sent synchronously under `Winkeyer.mu`, so on and off reach the radio in order. After `Close`, `processByte` drops any byte `Run` still reads, so nothing keys the radio again. The keyer sends a status byte whenever XOFF sets or clears, not only when busy changes. A MIDI controller that disconnects calls `Executor.DeviceLost`, which releases a held PTT but, unlike `Close`, leaves the executor open for the reconnect. Stop closes the inputs first, then the executor. RadioServer tracks each connection from accept, including while the HTTP/CAT mux peeks at its first bytes, so `Close` disconnects clients that never sent anything. The knob reader goroutine (`hidknob.Knob`) owns the WinUSB handle: reads use a 250 ms pipe timeout so `Close` returns promptly, a missing or not-set-up knob is retried every 2 s, each report is handed to the executor synchronously, and a waiting `Learn` takes the next usage through a one-shot channel instead of dispatching it. Stop closes the knob first, then MIDI, then the executor. An action that waits on the radio holds `Executor.mu` and so stalls both inputs — for example, a VFO tuning step with a cold cache blocks in `GetFreqA` for up to the CAT query's 500 ms timeout (`internal/cat/catclient.go`), while the MIDI callback keeps queueing events without blocking (`internal/midi/queue.go`): once 64 are pending it drops only Control Change (jog/knob steps), never notes (PTT release) or `MIM_CLOSE`; past a 4096-event memory cap it drops everything and the event loop calls `Executor.ReleasePTT`, since the lost message may have been a PTT release. K3Radio (`internal/cat/k3radio.go`) owns two goroutines. The KY sender polls `KY;` outside any lock and writes one character per "not full" reply while holding `kySender.sendMu`; `Abort` (RX) takes the same `sendMu`, so no character from an aborted message is written after `RX;`. Lock order: `sendMu` → `CATClient.writeMu`; `sendMu` is never taken while holding `queryMu`. The NR/notch toggler records requests and returns; its worker reads `DS` and taps `SWT34`/`SWT32` only on a difference, waiting up to 2 s for a tap to show, so RadioServer's per-connection loop never waits on it. A K3Radio query that times out marks re-init: under K3Radio's query lock, the next query first asks `K3;` — `K30` proves a power cycle, so it re-sends `K20;K31;AI0;` and drops NR/notch taps in flight; `K31` means the K3 was only slow and nothing is reset. The toggler's `DS` read runs this `K3;` check on every read, not only after a timeout, as one operation under the query lock: `DS` reports NR/notch only in `K31`, and an idle power cycle leaves the K3 in `K30` with no timeout to arm re-init, so a `K30` there restores the modes, drops pending taps and discards that read. `SetPTT(true)` sends `TX;` under `sendMu` and is refused once `Close` has begun (`K3Radio.closed`, set before its `RX;`), so no `TX;` follows shutdown's `RX;`; `NewK3Radio` sends `RX;` before the mode init, unkeying a transmitter left keyed before a restart. In headless `k3` mode, `CATClient.Done` closing (serial link lost) exits the process so systemd restarts it.

### Serial Port Configuration

WinKeyer protocol uses 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits). Reports as WinKeyer version 31 (WK3.1).
