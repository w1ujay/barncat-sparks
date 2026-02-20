# wk4

WinKeyer3 emulator and MIDI controller for the Elecraft K4 radio.

wk4 bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands. It also supports MIDI controllers (e.g., Numark DJ2GO2) for hands-on control of VFO tuning, volume, band switching, PTT, and more.

## How It Works

```
Logger (N1MM, etc.) <-> virtual serial port <-> wk4 <-> K4 radio (TCP)
MIDI controller (USB) <-> wk4 ----------------/
```

Both subsystems are optional and can run independently or together, sharing the same CAT connection to the K4.

## Requirements

- Go 1.21+
- A virtual serial port pair (for WinKeyer mode):
  - **Windows:** [com0com](https://com0com.sourceforge.net/)
  - **Linux:** `socat`
- Network access to your K4 (direct TCP or via a CAT server like QK4 on port 9299)
- **MIDI:** Windows only (uses `winmm.dll` directly — no CGO or external libraries needed)

## Building

```bash
go build -o wk4 .                          # Linux (WinKeyer only, MIDI stubs)
GOOS=windows go build -o wk4.exe .         # Windows (WinKeyer + MIDI)
```

No CGO required. The Windows build uses `winmm.dll` syscalls for MIDI, so cross-compilation works with just `GOOS=windows` — no mingw-w64 or C toolchain needed.

## Usage

```
wk4 [-port <serial-port>] [-midi <device>] [-cat host:port] [-speed wpm]
```

At least one of `-port` or `-midi` must be specified.

| Flag           | Default            | Description                              |
|----------------|--------------------|------------------------------------------|
| `-port`        | *(optional)*       | Serial port for WinKeyer interface       |
| `-midi`        | *(optional)*       | MIDI device name or substring            |
| `-midi-config` | `midi-config.json` | Path to MIDI mapping config file         |
| `-midi-list`   | `false`            | List available MIDI devices and exit     |
| `-cat`         | `localhost:9299`   | K4 CAT server address                    |
| `-speed`       | `25`               | Initial keyer speed in WPM               |

### Examples

```bash
# WinKeyer only (Windows with com0com)
wk4 -port COM5

# MIDI only
wk4 -midi DJ2GO2

# Both WinKeyer and MIDI
wk4 -port COM5 -midi DJ2GO2 -cat k4.local:9299

# List available MIDI devices
wk4 -midi-list

# Set initial speed to 30 WPM
wk4 -port COM5 -speed 30
```

### Setting Up Virtual Serial Ports

**Windows (com0com):**

Install com0com and create a port pair (e.g., COM4/COM5). Point N1MM at COM4, point wk4 at COM5.

**Linux (socat):**

```bash
socat -d -d pty,raw,echo=0 pty,raw,echo=0
```

This prints two `/dev/pts/N` paths. Point your logger at one, wk4 at the other.

## MIDI Controller Support

wk4 can connect a USB MIDI controller to control the K4. MIDI events (knob turns, button presses) are mapped to radio functions via a JSON config file. MIDI support requires Windows (`winmm.dll`); on other platforms the MIDI flags are accepted but return a "not supported" message.

### Supported Actions

| Action     | Description                          | MIDI Type        |
|------------|--------------------------------------|------------------|
| `vfo_tune` | Adjust VFO A frequency               | CC (relative 2's complement) |
| `volume`   | Set audio gain                        | CC (absolute)    |
| `speed`    | Set CW keyer speed (WPM)             | CC (absolute)    |
| `ptt`      | Push-to-talk (hold note = transmit)   | Note On/Off      |
| `band_up`  | Next band                             | Note On          |
| `band_down`| Previous band                         | Note On          |
| `vfo_a`    | Select VFO A                          | Note On          |
| `vfo_b`    | Select VFO B                          | Note On          |
| `vfo_swap` | Swap VFO A and B                      | Note On          |

### Configuration

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

- `device` — substring match against MIDI port names (use `wk4 -midi-list` to find yours)
- `type` — `"cc"` for Control Change, `"note_on"` for Note On
- `channel` — MIDI channel (0-15)
- `number` — CC number or note number
- `action` — one of the actions above
- `opts` — action-specific options (e.g., `step` for VFO tune Hz per tick)

## Protocol Details

- Implements K1EL WinKeyer binary protocol (reports as WK version 31 / WK3.1)
- Serial: 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits)
- 64-byte CW character buffer with XON/XOFF flow control
- Character echo timed to actual WPM using PARIS standard (1 dit = 1200/WPM ms)
- CW text sent in word-sized chunks (split on spaces) for responsive abort — ESC stops after the current word
- Supported CAT commands: `KY` (CW text), `KS` (speed), `TX`/`RX` (PTT), `FA`/`FB` (VFO frequency), `AG` (volume), `BU`/`BD` (band), `FT`/`SWT` (VFO select)

## License

See [LICENSE](LICENSE) for details.
