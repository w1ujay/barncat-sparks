# BarnCat Sparks

WinKeyer3 emulator and MIDI controller for the Elecraft K4 radio, with remote support for the K3.

BarnCat Sparks bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands. It also supports MIDI controllers (e.g., Numark DJ2GO2) and a USB tuning knob for hands-on control of VFO tuning, volume, band switching, PTT, and more.

Part of the **BarnCat** family of ham radio software.

## How It Works

```
Logger (N1MM, etc.) <-> virtual serial port <-> sparks <-> K4 radio (TCP or serial)
MIDI controller (USB) <-> sparks ----------------/
USB knob (WinUSB) <-> sparks --------------------/
Browser / WebView2 <-> sparks (config/control) --/
```

Both subsystems are optional and can run independently or together, sharing the same CAT connection to the K4.

## Install

Download the latest Windows zip from the [Releases](https://github.com/w1ujay/barncat-sparks/releases) page, unzip it, and run `barncat-sparks.exe`. A Linux arm64 build for the Raspberry Pi is published alongside it.

## Requirements

- A virtual serial port pair (for WinKeyer mode):
  - **Windows:** [com0com](https://com0com.sourceforge.net/)
  - **Linux:** `socat`
- Network access to your K4 (direct TCP port 9200, authenticated port 9205, or via a CAT server)
- **MIDI:** Windows only (uses `winmm.dll` directly — no CGO or external libraries needed)
- **USB knob:** Windows only, after a one-time driver swap (see [`docs/knob-setup.md`](docs/knob-setup.md))
- **GUI:** Windows uses a native WebView2 window (preinstalled on Windows 10/11). Other platforms use Chrome/Edge `--app` mode or the default browser.

## Building

Requires Go 1.27+.

```bash
# Build for current platform
go build -o barncat-sparks ./cmd/barncat-sparks/

# Cross-compile for Windows (GUI, no console window)
GOOS=windows go build -ldflags "-H windowsgui" -o barncat-sparks.exe ./cmd/barncat-sparks/

# Or use the build script
./build.sh

# Generate Windows icon/manifest resources (only needed after changing winres/)
go install github.com/tc-hib/go-winres@v0.3.3
(cd cmd/barncat-sparks && go-winres make)

# Raspberry Pi (CGO_ENABLED=0 is required on WSL)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o barncat-sparks-arm64 ./cmd/barncat-sparks/

# Tests
go test ./...
go vet ./...
```

No CGO required. The Windows build uses `winmm.dll` syscalls for MIDI and `jchv/go-webview2` (pure Go COM calls) for the native window — cross-compilation works with just `GOOS=windows`.

Pushing a `v*` tag builds the Windows and Pi releases and publishes them to GitHub (`.github/workflows/release.yml`). Vendor manuals are kept locally in `docs/reference/`, which git ignores.

## Usage

### Web UI (default)

```bash
barncat-sparks
```

Double-click or run without flags to launch the web UI. Configure serial port, CAT address, MIDI device, and mappings in a single window. Settings are saved to `sparks-config.json` next to the executable, including the K4 password and hearth token in plain text, so keep that file private.

### Backups

Each save first copies the old settings into a `sparks-config-backups` folder beside the config, keeping the last 20 per config. To go back, stop Sparks, pick a date under **Backups** at the bottom of Settings, and click **Restore**. Your current settings are backed up first, so a restore can be undone. The backups hold your password and token too, so treat them like the config.

If Sparks can't read your settings file, it shows a warning and turns off Save and Start instead of writing blank settings over it. Closing and reopening Sparks clears a temporary read problem, and restoring a backup fixes a damaged file.

### Updates

At startup Sparks asks GitHub for the latest release and shows **Update available** at the top of the window if it's newer. Click the link for the release notes. Pre-releases are skipped, and with no internet nothing is shown.

On Windows, stop Sparks and click **Update now**. Sparks downloads the zip, verifies it against the release's `checksums.txt`, replaces `barncat-sparks.exe`, and reopens with your settings. Only the program is replaced: the `README`, `CHANGELOG`, `LICENSE` and notices beside it stay as they were.

To go back, close Sparks, delete `barncat-sparks.exe`, and rename `barncat-sparks.exe.old` to `barncat-sparks.exe`. Each update replaces the `.old` file, so only the previous version is kept. If you run a second copy from the same folder, close it before updating.

The Raspberry Pi build doesn't update itself: copy the new release over by hand, as in [`docs/pi-setup.md`](docs/pi-setup.md).

### Running a Second Copy

On Windows, only one copy can use a config at a time: opening a second shows "already running" and exits. To run a second copy on purpose, give it its own file with `-config`.

### Headless (CLI)

```bash
barncat-sparks -port COM5 -midi DJ2GO2 -cat k4.local:9200
```

CLI mode activates when `-port`, `-midi`, or `-nogui` flags are present.

| Flag             | Default            | Description                              |
|------------------|--------------------|------------------------------------------|
| `-port`          | *(optional)*       | Serial port for WinKeyer interface       |
| `-midi`          | *(optional)*       | MIDI device name or substring            |
| `-midi-config`   | `midi-config.json` | Path to MIDI mapping config file         |
| `-midi-list`     | `false`            | List available MIDI devices and exit     |
| `-cat`           | `localhost:9299`   | K4 CAT server address                    |
| `-cat-transport` | `tcp`              | CAT transport: `tcp` or `serial`         |
| `-cat-serial`    | *(optional)*       | Serial port for radio CAT (e.g., COM3)  |
| `-cat-baud`      | `38400`            | Baud rate for serial CAT                 |
| `-cat-password`  | *(optional)*       | K4 authentication password (port 9205)   |
| `-cat-listen`    | *(optional)*       | TCP listen address for CAT relay server  |
| `-radio-listen`  | *(optional)*       | TCP listen address for RadioServer       |
| `-radio-model`   | `k4`               | `k4` or `k3` (`k3` needs serial CAT)     |
| `-radio-log-raw` | `false`            | Log commands RadioServer passes through  |
| `-speed`         | `25`               | Initial keyer speed in WPM               |
| `-config`        | *(next to exe)*    | Path to the config file                  |
| `-nogui`         | `false`            | Run in headless mode                     |
| `-version`       | `false`            | Print version and exit                   |

### Setting Up Virtual Serial Ports

**Windows (com0com):**

Install com0com and create a port pair (e.g., COM4/COM5). Point N1MM at COM4, point barncat-sparks at COM5.

**Linux (socat):**

```bash
socat -d -d pty,raw,echo=0 pty,raw,echo=0
```

This prints two `/dev/pts/N` paths. Point your logger at one, barncat-sparks at the other.

## MIDI Controller Support

BarnCat Sparks can connect a USB MIDI controller to control the K4. MIDI events (knob turns, button presses) are mapped to radio functions via the web UI or a JSON config file. MIDI support requires Windows (`winmm.dll`). See [`docs/dj2go2-sparks-mapping.md`](docs/dj2go2-sparks-mapping.md) for a full DJ2GO2 layout.

### Learn

To map a control in the web UI, tick your device under MIDI, open **Mappings**, and add a row. Pick an action, click **Learn**, then press or move the control within 10 seconds. The row's type, channel and number fill in. Learn works whether Sparks is running or stopped, and the press you use doesn't run that control's action. Save, then Stop and Start if Sparks is running, since mappings load at Start.

### Supported Actions

| Action          | Description                    | MIDI Type              |
|-----------------|--------------------------------|------------------------|
| `vfo_tune`      | VFO A frequency adjust         | CC (relative)          |
| `vfo_b_tune`    | VFO B frequency adjust         | CC (relative)          |
| `volume`        | AF gain (main)                 | CC (absolute)          |
| `volume_b`      | AF gain (sub)                  | CC (absolute)          |
| `rf_gain`       | RF gain (main)                 | CC (absolute)          |
| `rf_gain_b`     | RF gain (sub)                  | CC (absolute)          |
| `rf_gain_adjust` | RF gain adjust (main)         | CC (relative)          |
| `rf_gain_b_adjust` | RF gain adjust (sub)        | CC (relative)          |
| `rf_gain_up`    | RF gain up (main)              | Note On                |
| `rf_gain_down`  | RF gain down (main)            | Note On                |
| `rf_gain_b_up`  | RF gain up (sub)               | Note On                |
| `rf_gain_b_down` | RF gain down (sub)            | Note On                |
| `rf_gain_toggle` | RF gain toggle (main)         | Note On                |
| `rf_gain_b_toggle` | RF gain toggle (sub)        | Note On                |
| `power`         | TX power (0.1W-110W)           | CC (absolute)          |
| `speed`         | CW keyer speed (WPM)           | CC (absolute)          |
| `filter_bw`     | Filter bandwidth (absolute)    | CC (absolute)          |
| `filter_adjust` | Filter bandwidth adjust        | CC (relative)          |
| `filter_up`     | Filter wider (button)          | Note On                |
| `filter_down`   | Filter narrower (button)       | Note On                |
| `notch`         | Manual notch toggle            | Note On                |
| `notch_freq`    | Manual notch frequency         | CC (absolute)          |
| `ptt`           | Push-to-talk (hold = TX)       | Note On/Off            |
| `band_up`       | Next band                      | Note On                |
| `band_down`     | Previous band                  | Note On                |
| `vfo_a`         | Select VFO A                   | Note On                |
| `vfo_b`         | Select VFO B                   | Note On                |
| `vfo_swap`      | Swap VFO A/B                   | Note On                |
| `vfo_a_to_b`    | Copy VFO A → B                 | Note On                |
| `vfo_b_to_a`    | Copy VFO B → A                 | Note On                |
| `rit`           | RIT toggle                     | Note On                |
| `xit`           | XIT toggle                     | Note On                |
| `rit_clear`     | Clear RIT offset               | Note On                |
| `nb`            | Noise blanker toggle           | Note On                |
| `nr`            | Noise reduction toggle         | Note On                |
| `split`         | Split toggle                   | Note On                |
| `cw_msg`        | Send CW message                | Note On                |

### Configuration (headless mode)

Edit `midi-config.json` to map your controller's MIDI events to actions:

```json
{
  "device": "DJ2GO2",
  "mappings": [
    { "type": "cc",      "channel": 0, "number": 1,  "action": "vfo_tune", "opts": { "step": 10 } },
    { "type": "cc",      "channel": 0, "number": 7,  "action": "volume" },
    { "type": "note_on", "channel": 0, "number": 36, "action": "ptt" }
  ]
}
```

### USB Tuning Knob

A DROK/iWit USB knob can run the same actions as MIDI through **Knob** mapping rows, with its own Learn button. See [`docs/knob-setup.md`](docs/knob-setup.md).

### MIDI Volume to QK4

Under Options, **Route MIDI volume to QK4** sends MIDI volume to a QK4 instead of the radio.

## Remote K3

> **Not yet tested on a real K3.** This feature is built and passes its automated tests, but it hasn't been tried on the air.

Sparks can run headless on a Raspberry Pi next to an Elecraft K3 and present it over the tailnet as a K4, so home Sparks and N1MM connect to it the same way:

```bash
barncat-sparks -nogui -radio-model k3 -cat-transport serial -cat-serial <port> -radio-listen <tailnet-ip>:9400
```

RadioServer has no login, so only bind it to a private address such as a tailnet IP. Update home Sparks and the Pi together. See [`docs/pi-setup.md`](docs/pi-setup.md).

## Station (lempster-hearth)

The Station section of the web UI shows the cabin K4's power state and its
Starlink power-save status, and lets you power the K4 on/off and arm/disarm
power-save. It talks to lempster-hearth running on the cabin's Pi, not to the
K4 directly.

### Settings

| Field           | Description                                                              |
|-----------------|---------------------------------------------------------------------------|
| Hearth URL      | The cabin Pi's hearth API, reached over the tailnet, e.g. `http://<cabin-pi>:8123` |
| Replica URL     | Optional hosted replica, used when the Pi is unreachable, e.g. `https://<replica-host>` |
| Site            | Needed when the replica is a multi-site hub: the site name, e.g. `cabin`. Sent as `?site=` on status and power-save requests; a single-site Pi ignores it. |
| Operator Token  | Issued by the site owner, scoped to `k4` and/or `powersave`. Press Test to see the token's name and scopes. |

### Access

The operator needs tailnet access to the cabin Pi's hearth port and to the
K4's CAT port; the site owner is responsible for exposing hearth on the
tailnet and issuing tokens.

### When the cabin is dark

Power-save blacks out the cabin's Starlink link on a schedule. While it's
down, status is read from the replica instead of the Pi, and a power-save
change you make is queued there until the cabin's next check-in. K4 controls
need the Pi directly and are unavailable during a blackout.

## Protocol Details

- Implements the [K1EL WinKeyer serial protocol](https://www.k1elsystems.com/WK3IC.html) (reports as WK version 31 / WK3.1)
- Serial: 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits)
- 64-byte CW character buffer with XON/XOFF flow control
- Character echo timed to actual WPM using PARIS standard (1 dit = 1200/WPM ms)
- CW text sent in word-sized chunks (split on spaces) for responsive abort
- K4 CAT commands per the [K4 Programmer's Reference](https://elecraft.com/pages/programmers-reference-manuals)

### Acknowledgment

The WinKeyer serial protocol was designed by Steven T. Elliott, [K1EL](https://www.k1elsystems.com/).
WinKeyer is a product of K1EL Systems. BarnCat Sparks is not affiliated with or endorsed by K1EL.

## License

[MIT](LICENSE)
