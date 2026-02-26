# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- RTTY/FSK support via WinKeyer admin command 0x13 — N1MM can send RTTY text through bracket delimiters
- CW/RTTY text decode panel in web UI (polls K4 text decoder via TT command)
- Application versioning with build-time injection (`-ldflags "-X github.com/ujay/barncat-sparks/internal/config.Version=X.Y.Z"`)
- `-version` flag to print version and exit
- Version displayed in web UI header and page title
- App-mode browser window (chromeless Chrome/Edge `--app` mode) for native GUI feel
- Fallback to default browser when Chrome/Edge is not available
- Graceful shutdown: closing the browser window exits the program, Ctrl+C closes the browser
- CHANGELOG.md

### Changed
- Reorganized codebase into Go packages: `internal/cat`, `internal/keyer`, `internal/midi`, `internal/config`, `internal/webui`, and `cmd/barncat-sparks`
- Minimum Go version bumped from 1.21 to 1.24
- Build target changed from `.` to `./cmd/barncat-sparks/`

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
