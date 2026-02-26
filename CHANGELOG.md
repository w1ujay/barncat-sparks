# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- LP-100A wattmeter serial driver (`internal/lp100a`) — polls power, SWR, impedance at 200ms intervals with peak SWR tracking (4s timeout)
- LP-100A HTTP API on RadioServer port (`GET /api/lp100a`, `POST /api/lp100a/command`) — coexists with amp proxy and CAT on the same TCP connection
- LP-100A serial port selector in web UI and `sparks-config.json` (`lp100_port`)
- RF-Kit amplifier HTTP proxy on RadioServer port — multiplexes HTTP and CAT on the same TCP connection by peeking at the first bytes of each connection
- Amplifier URL configuration field in web UI and `sparks-config.json` (`amp_url`)

### Changed
- RadioServer HTTP routing refactored from single handler to `http.ServeMux` with `Handle()` method for registering multiple HTTP handlers on the same port

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
