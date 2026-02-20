# wk4

WinKeyer3 emulator for the Elecraft K4 radio.

wk4 bridges contest logging software (N1MM Logger, etc.) that speaks the K1EL WinKeyer binary serial protocol to a K4 radio via TCP CAT commands. This lets your logger use its native CW keying engine for remote K4 operation.

## How It Works

```
Logger (N1MM, etc.) <-> virtual serial port <-> wk4 <-> K4 radio (TCP)
```

You create a virtual serial port pair, point your logger at one end and wk4 at the other. wk4 translates WinKeyer protocol commands into Elecraft CAT commands (`KY`, `KS`, `TX`/`RX`) sent over TCP.

## Requirements

- Go 1.21+
- A virtual serial port pair:
  - **Windows:** [com0com](https://com0com.sourceforge.net/)
  - **Linux:** `socat`
- Network access to your K4 (direct TCP or via a CAT server like QK4 on port 9299)

## Building

```bash
go build -o wk4 .                     # Linux
GOOS=windows go build -o wk4.exe .    # Cross-compile for Windows
```

## Usage

```
wk4 -port <serial-port> [-cat host:port] [-speed wpm]
```

| Flag     | Default          | Description                              |
|----------|------------------|------------------------------------------|
| `-port`  | *(required)*     | Serial port for WinKeyer interface       |
| `-cat`   | `localhost:9299` | K4 CAT server address                    |
| `-speed` | `25`             | Initial keyer speed in WPM               |

### Examples

```bash
# Windows with com0com virtual port pair
wk4 -port COM5

# Linux with socat, K4 on the network
wk4 -port /dev/pts/3 -cat k4.local:9299

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

## Protocol Details

- Implements K1EL WinKeyer binary protocol (reports as WK version 31 / WK3.1)
- Serial: 1200 baud, 8N2 (8 data bits, no parity, 2 stop bits)
- 64-byte CW character buffer with XON/XOFF flow control
- Character echo timed to actual WPM using PARIS standard (1 dit = 1200/WPM ms)
- Supported CAT commands: `KY` (CW text), `KS` (speed), `TX`/`RX` (PTT)

## License

See [LICENSE](LICENSE) for details.
