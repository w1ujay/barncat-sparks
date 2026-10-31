# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Remote K3: run Sparks headless on a Raspberry Pi next to an Elecraft K3 (`-radio-model k3`) and connect home Sparks and N1MM (set to K4) to it over the tailnet like a K4 — CW from N1MM, knob/MIDI controls, and frequency/mode readback. See `docs/pi-setup.md`
- Linux arm64 release build for the Pi
- MIDI Learn: CC and Note On mapping rows have a Learn button, as Knob rows do. Click it, then press or move the control on your DJ2GO2 or other MIDI controller within 10 seconds, and the row's type, channel and number fill in. It works running or stopped, and a press used for Learn doesn't run that control's action
- Config backups: each save keeps the previous settings in `sparks-config-backups` (last 20), with a Restore control at the bottom of Settings
- On Windows, opening Sparks a second time with the same config now shows "already running" and exits, so a second window can't save over your settings. A copy using its own `-config` file still runs

### Security
- Built with Go 1.27.3, which fixes standard-library vulnerabilities reported by govulncheck
- The local web UI now refuses requests that didn't come from its own page, so a website open on the same PC can't change settings, start or stop Sparks, or read the K4 password and hearth token

### Fixed
- Saving settings can no longer leave a damaged config: the new file is written separately and swapped in
- If the config can't be read, Sparks now warns and blocks Save and Start, instead of showing blank settings that could overwrite your real ones
- Band Up and Band Down now change band on the K4. They were sending `BU;` and `BD;`, which the K4 doesn't have, instead of `BN+;` and `BN-;`
- RadioServer no longer drops commands it doesn't model: N1MM's IF/SB/DV polling and mode changes other than CW and DATA (USB, LSB, CW-REV, DATA-REV) now reach the radio, and their replies come back
- Sub-receiver queries such as NB$, NR$, RT$, XT$ and NM$ no longer switch that function off on the main receiver when sent to RadioServer
- RadioServer answers a client's `AI;` with `AI0;` and no longer forwards `AI` sets to the radio, so a logger asking for auto-info can't make the K4 push unsolicited replies into Sparks's session

## [0.3.2] - 2026-09-27

### Added
- Operate tab Power-save row shows "armed · held up: K4 on" in amber when hearth holds the Starlink dish up because the rig is on, with no "down until" countdown or "cabin dark" on the replica status line while the dish stays up; via the replica it adds "(as of last contact)" once the push is over 120 s old. Needs hearth a2463c4 or later — older builds show the row as before
- Arming power-save with the K4 on during a down window now says the dish stays up while the K4 is on, instead of warning that the link drops immediately

### Fixed
- Power-save no longer shows "arm queued" or "disarm queued" from the replica once the cabin already matches it — hearth keeps the last request as standing state after the cabin applies it
- "Pulsed power — waiting for the rig to come up" clears once the Pi reads the K4 on (an older replica snapshot doesn't count)

## [0.3.1] - 2026-09-27

### Added
- Operate tab Station panel shows the main battery from hearth — charge level plus charging/discharging power (e.g. "87% · charging +120 W"), marked stale when the reading is old, and hidden when the site has no battery

### Fixed
- Stop, clear buffer and a logger disconnect now release PTT keyed by WinKeyer buffered PTT (0x18), and a logger serial port that fails mid-tune or mid-RTTY-bracket unkeys the radio instead of leaving it transmitting
- A MIDI jog spun while the radio is slow to answer can no longer crowd out a PTT release or a controller disconnect — only jog/knob steps are dropped when events back up, and PTT is released if anything else ever is
- Stop no longer hangs when the radio link stalls mid-write — CAT writes time out after 2 s and closing the link doesn't wait for them
- MIDI and knob commands reach the radio in the order they were made — a quick volume-up then mute no longer ends at full volume
- The last steps of a fast VFO spin are no longer lost to the tuning throttle — they're sent once it lapses, on VFO A and B
- RTTY messages no longer lose their last word — PTT is held until all queued text is on the air, counting FIGS/LTRS shifts plus a 1.5 s pad, so a bracket sent while the previous one is still transmitting no longer drops to RX early

## [0.3.0] - 2026-09-14

### Added
- K4 direct authentication (SHA-384) — connect to K4 port 9205 without QK4 bridge, with TCP mode dropdown in web UI (Plain TCP / K4 Auth / K4 TLS-PSK disabled) and `-cat-password` CLI flag
- LP-100A wattmeter serial driver (`internal/lp100a`) — polls power, SWR, impedance at 200ms intervals with peak SWR tracking (4s timeout)
- LP-100A HTTP API on RadioServer port (`GET /api/lp100a`, `POST /api/lp100a/command`) — coexists with amp proxy and CAT on the same TCP connection
- LP-100A serial port selector in web UI and `sparks-config.json` (`lp100_port`)
- Multi-device MIDI support — configure multiple MIDI devices simultaneously, all sharing the same mapping dispatch
- RF-Kit amplifier HTTP proxy on RadioServer port — multiplexes HTTP and CAT on the same TCP connection by peeking at the first bytes of each connection
- Amplifier URL configuration field in web UI and `sparks-config.json` (`amp_url`)
- DJ2GO2 MIDI control name registry (`internal/config/controls.go`) — maps MIDI type/channel/number to physical control names (Jog Wheel, Play, Hot Cue, etc.)
- Control names shown in mapping cards (e.g., "VFO Tune — Jog Wheel A") so mappings are identifiable at a glance
- QK4 volume routing — optional checkbox to route MIDI volume commands to a local QK4 instance instead of the direct K4 connection, with configurable address (default `127.0.0.1:9299`)
- Station section in the web UI — shows the cabin K4's power state and Starlink power-save via lempster-hearth, with K4 On/Off and power-save Arm/Disarm; authenticates with a per-operator hearth token (`hearth_url`, `hearth_replica_url`, `hearth_token`)
- Replica fallback for the Station — when the cabin Pi is unreachable, status comes from the hosted replica and power-save changes are queued for the cabin's next check-in; set the Station's Site (`hearth_site`, e.g. `cabin`) when the replica is a multi-site hub
- DROK/iWit USB knob as a mappable control — read directly over WinUSB (one-time Zadig driver swap, see `docs/knob-setup.md`), shown in the MIDI device list, with Knob mapping rows, a Learn button and +/− direction for step actions; works whichever window has focus
- Laptop wake from the web UI — the Station card lists hearth's wake-on-LAN targets with awake/asleep status and a Wake button (needs the hearth `wol` operator scope); status is polled every 60s while Operate is visible and every 5s for up to 5 minutes after a wake

### Changed
- Web UI split into Operate and Settings tabs — Start/Stop, status and the station controls (K4, power-save, laptop) are on Operate; all configuration and Save Settings are on Settings
- MIDI device selector changed from add/remove dropdowns to checkboxes of available devices — check which devices to connect, uncheck to skip
- RadioServer HTTP routing refactored from single handler to `http.ServeMux` with `Handle()` method for registering multiple HTTP handlers on the same port
- Go 1.27 (`go 1.27.0`, toolchain `go1.27.1`); `go.bug.st/serial` v1.8.0 and `golang.org/x/sys` v0.48.0 (fixes GO-2026-5024); `creack/goselect` no longer needed
- Action execution moved from the MIDI driver to a shared `internal/controls` executor used by MIDI and the knob; `/api/actions` now reports each action's kind

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
- WinKeyer input read after Stop is ignored, so a pending tune (key immediate), CW or RTTY `[` can no longer key the radio after it was released
- Unplugging a MIDI controller while its PTT pad is held releases PTT; the pads key the radio again once it reconnects
- WinKeyer reports XOFF and XON as the buffer fills and drains, so N1MM pauses instead of overrunning the 64-character buffer; the buffer reuses slots already sent, so text sent after XON is no longer dropped
- WinKeyer backspace removes the last unsent character instead of sending it anyway
- RadioServer passes RTTY messages longer than 24 characters through intact instead of cutting them at 24
- RadioServer Stop disconnects a client that connected but never sent anything while the amp proxy or LP-100A HTTP is enabled, instead of leaving its socket open
- Headless mode (`-nogui`) runs with only `-cat-listen` or `-radio-listen` instead of printing usage and exiting

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
