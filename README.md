# wk4

WinKeyer3 emulator and MIDI controller for the Elecraft K4 radio.

wk4 bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands. It also supports MIDI controllers (e.g., Numark DJ2GO2) for hands-on control of VFO tuning, volume, band switching, PTT, and more.

## How It Works

```
Logger (N1MM, etc.) <-> virtual serial port <-> wk4 <-> K4 radio (TCP or serial)
MIDI controller (USB) <-> wk4 ----------------/
Browser / WebView2 <-> wk4 (config/control) --/
```

Both subsystems are optional and can run independently or together, sharing the same CAT connection to the K4.

## Requirements

- Go 1.21+
- A virtual serial port pair (for WinKeyer mode):
  - **Windows:** [com0com](https://com0com.sourceforge.net/)
  - **Linux:** `socat`
- Network access to your K4 (direct TCP port 9200, or via a CAT server)
- **MIDI:** Windows only (uses `winmm.dll` directly — no CGO or external libraries needed)
- **GUI:** Windows uses a native WebView2 window (preinstalled on Windows 10/11). Other platforms use Chrome/Edge `--app` mode or the default browser.

## Building

```bash
# Build for current platform
go build -o wk4 .

# Cross-compile for Windows (GUI, no console window)
GOOS=windows go build -ldflags "-H windowsgui" -o wk4.exe .

# Or use the build script
./build.sh

# Generate Windows icon/manifest resources (only needed after changing winres/)
go install github.com/tc-hib/go-winres@latest
go-winres make

# Tests
go test ./...
go vet ./...
```

No CGO required. The Windows build uses `winmm.dll` syscalls for MIDI and `jchv/go-webview2` (pure Go COM calls) for the native window — cross-compilation works with just `GOOS=windows`.

## Usage

### Web UI (default)

```bash
wk4
```

Double-click or run without flags to launch the web UI. Configure serial port, CAT address, MIDI device, and mappings in a single window. Settings are saved to `wk4-config.json`.

### Headless (CLI)

```bash
wk4 -port COM5 -midi DJ2GO2 -cat k4.local:9200
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
| `-cat-listen`    | *(optional)*       | TCP listen address for CAT relay server  |
| `-speed`         | `25`               | Initial keyer speed in WPM               |
| `-nogui`         | `false`            | Run in headless mode                     |

### Setting Up Virtual Serial Ports

**Windows (com0com):**

Install com0com and create a port pair (e.g., COM4/COM5). Point N1MM at COM4, point wk4 at COM5.

**Linux (socat):**

```bash
socat -d -d pty,raw,echo=0 pty,raw,echo=0
```

This prints two `/dev/pts/N` paths. Point your logger at one, wk4 at the other.

## MIDI Controller Support

wk4 can connect a USB MIDI controller to control the K4. MIDI events (knob turns, button presses) are mapped to radio functions via the web UI or a JSON config file. MIDI support requires Windows (`winmm.dll`).

### Supported Actions

| Action          | Description                    | MIDI Type              |
|-----------------|--------------------------------|------------------------|
| `vfo_tune`      | VFO A frequency adjust         | CC (relative)          |
| `vfo_b_tune`    | VFO B frequency adjust         | CC (relative)          |
| `volume`        | AF gain (main)                 | CC (absolute)          |
| `volume_b`      | AF gain (sub)                  | CC (absolute)          |
| `rf_gain`       | RF gain (main)                 | CC (absolute)          |
| `rf_gain_b`     | RF gain (sub)                  | CC (absolute)          |
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

## Protocol Details

- Implements K1EL WinKeyer binary protocol (reports as WK version 31 / WK3.1)
- Serial: 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits)
- 64-byte CW character buffer with XON/XOFF flow control
- Character echo timed to actual WPM using PARIS standard (1 dit = 1200/WPM ms)
- CW text sent in word-sized chunks (split on spaces) for responsive abort
- K4 CAT commands per the [K4 Programmer's Reference](https://elecraft.com/pages/programmers-reference-manuals)

## License

[MIT](LICENSE)
