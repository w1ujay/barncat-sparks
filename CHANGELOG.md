# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- K4 direct authentication (SHA-384) — connect to K4 port 9205 without QK4 bridge, with TCP mode dropdown in web UI (Plain TCP / K4 Auth / K4 TLS-PSK disabled) and `-cat-password` CLI flag
- LP-100A wattmeter serial driver (`internal/lp100a`) — polls power, SWR, impedance at 200ms intervals with peak SWR tracking (4s timeout)
- LP-100A HTTP API on RadioServer port (`GET /api/lp100a`, `POST /api/lp100a/command`) — coexists with amp proxy and CAT on the same TCP connection
- LP-100A serial port selector in web UI and `sparks-config.json` (`lp100_port`)
- HaliKey MIDI paddle support — `dit_paddle` and `dah_paddle` actions send K4 `KZ` element commands
- Paddle swap option (`paddle_swap`) in config and web UI to reverse dit/dah input
- Multi-device MIDI support — configure multiple MIDI devices simultaneously (e.g., DJ2GO2 + HaliKey), all sharing the same mapping dispatch
- RF-Kit amplifier HTTP proxy on RadioServer port — multiplexes HTTP and CAT on the same TCP connection by peeking at the first bytes of each connection
- Amplifier URL configuration field in web UI and `sparks-config.json` (`amp_url`)
- DJ2GO2 MIDI control name registry (`internal/config/controls.go`) — maps MIDI type/channel/number to physical control names (Jog Wheel, Play, Hot Cue, etc.)
- Control names shown in mapping cards (e.g., "VFO Tune — Jog Wheel A") so mappings are identifiable at a glance
- QK4 volume routing — optional checkbox to route MIDI volume commands to a local QK4 instance instead of the direct K4 connection, with configurable address (default `127.0.0.1:9299`)
- Station section in the web UI — shows the cabin K4's power state and Starlink power-save via lempster-hearth, with K4 On/Off and power-save Arm/Disarm; authenticates with a per-operator hearth token (`hearth_url`, `hearth_replica_url`, `hearth_token`)
- Replica fallback for the Station — when the cabin Pi is unreachable, status comes from the hosted replica and power-save changes are queued for the cabin's next check-in; set the Station's Site (`hearth_site`, e.g. `cabin`) when the replica is a multi-site hub

### Changed
- MIDI device selector changed from add/remove dropdowns to checkboxes of available devices — check which devices to connect, uncheck to skip
- RadioServer HTTP routing refactored from single handler to `http.ServeMux` with `Handle()` method for registering multiple HTTP handlers on the same port
- Go 1.27 (`go 1.27.0`, toolchain `go1.27.1`); `go.bug.st/serial` v1.8.0 and `golang.org/x/sys` v0.48.0 (fixes GO-2026-5024); `creack/goselect` no longer needed

### Fixed
- WinKeyer status byte is sent only when it changes, and Host Open replies with only the version byte — N1MM no longer reads a stray idle status at message start as "message complete" and drops text
- Stopping mid-transmission releases the radio: CW in progress is aborted and RTTY/tune PTT is dropped before the CAT link closes
- CW input no longer stops after 64 characters when the logger never clears the buffer — the WinKeyer buffer is reclaimed once drained
- A connection arriving while CATServer or RadioServer shuts down is closed instead of crashing the app
- Concurrent radio queries (RadioServer clients, MIDI VFO tuning) no longer time out by taking each other's replies
- A WinKeyer port failure during Start now stops CATServer, RadioServer, and the LP-100A, so a retry can bind the same ports
- RadioServer recognizes HTTP requests whose first bytes arrive in separate reads instead of treating them as CAT
- Stop and N1MM's clear buffer (Esc) no longer let a CW `KY` already in flight reach the radio after the abort `RX;`
- After an RTTY message finishes, Stop and the next clear buffer no longer send a stray `RX;` that could unkey another transmission
- MIDI PTT press and release reach the radio in order, so a quick tap can no longer leave the K4 transmitting
- Stopping MIDI while a PTT pad is held releases PTT before the CAT link closes, and a press arriving during shutdown no longer keys the radio
- A CATServer client that stops reading no longer stalls radio responses for queries, text decode and other clients — it is disconnected instead
- WinKeyer tune (key immediate) on and off reach the radio in order, so a quick toggle can't leave the K4 transmitting
- WinKeyer clear buffer (N1MM Esc) now stops a tune carrier
- WinKeyer reset and host close release everything still keying the radio — a pending RTTY PTT release, a tune carrier, and CW in progress
- WinKeyer unpause restarts CW that was queued while paused

## [0.2.1] - 2026-02-26

### Added
- `RadioServer` — protocol-translating TCP server that wraps any `Radio` interface and serves K4-compatible CAT over TCP, enabling future K3 support and remote operating
- GoReleaser configuration for automated Windows builds and GitHub Releases

## [0.2.0] - 2026-02-25

### Added
- RTTY/FSK support via WinKeyer admin command 0x13 — N1MM can send RTTY text through bracket delimiters
- CW/RTTY text decode panel in web UI (polls K4 text decoder via TT command)
- RF gain MIDI controls: encoder adjust, step up/down buttons, and toggle (RG/;) for main and sub receivers
- Application versioning with build-time injection (`-ldflags "-X github.com/ujay/barncat-sparks/internal/config.Version=X.Y.Z"`)
- `-version` flag to print version and exit
- Version displayed in web UI header and page title
- App-mode browser window (chromeless Chrome/Edge `--app` mode) for native GUI feel
- Fallback to default browser when Chrome/Edge is not available
- Graceful shutdown: closing the browser window exits the program, Ctrl+C closes the browser
- MIDI device rescan button in web UI (hot-plug without restart)
- `Radio` interface (`internal/cat/radio.go`) decoupling subsystems from concrete CATClient
- CHANGELOG.md

### Changed
- Reorganized codebase into Go packages: `internal/cat`, `internal/keyer`, `internal/midi`, `internal/config`, `internal/webui`, and `cmd/barncat-sparks`
- Minimum Go version bumped from 1.21 to 1.24
- Build target changed from `.` to `./cmd/barncat-sparks/`
- CW chunk size increased to 24 chars (K4 KY max) to reduce inter-chunk gaps
- Winkeyer, MIDIDriver, and WebUI now depend on `cat.Radio` interface instead of `*cat.CATClient`

### Fixed
- Removed auto-PTT (TX/RX) from CW sendLoop — K4 break-in handles PTT, eliminates VOX warnings
- Fixed race condition where clear buffer + immediate new text could drop characters (sendLoop cancel check under lock)

## [0.1.0] - 2025-01-01

### Added
- WinKeyer3 protocol emulation (version 31) over serial port
- K4 CAT client with TCP and serial transport
- CW character buffer with XON/XOFF flow control
- KY command generation with word-chunked sending
- Character echo timing based on WPM (PARIS standard)
- CAT server for TCP relay to serial radio
- MIDI controller support (CC, Note On/Off) for K4 functions
- VFO tuning, volume, speed, PTT, band up/down, VFO A/B/swap actions
- Action registry with configurable options per mapping
- Web UI with dark theme for configuration and control
- Serial port and MIDI device enumeration
- Config persistence to `sparks-config.json`
- Headless CLI mode (backwards compatible with original interface)
- Protocol trace logging for debugging WinKeyer communication
