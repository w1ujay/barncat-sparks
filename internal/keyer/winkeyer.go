package keyer

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ujay/barncat-sparks/internal/cat"
	"go.bug.st/serial"
)

const (
	WKVersion = 31 // Report as WK3.1

	BufferSize    = 64
	XoffThreshold = 42 // Set XOFF when buffer >= 2/3 full
	XonThreshold  = 20 // Clear XOFF when buffer drops below this
	KYChunkSize   = 24 // Max chars per KY command (K4 limit; larger chunks reduce inter-KY gaps)

	// Status byte flags (OR'd with 0xC0)
	StatusXoff    byte = 0x01
	StatusBreak   byte = 0x02
	StatusBusy    byte = 0x04
	StatusKeyDown byte = 0x08
	StatusWait    byte = 0x10
)

// Parser states
const (
	stIdle = iota
	stAdmin
	stAdminParam
	stParam
)

// Winkeyer implements a K1EL WinKeyer3 emulator that translates
// the Winkeyer binary protocol into K4 CAT commands (KY, KS, etc).
type Winkeyer struct {
	port serial.Port
	cat  cat.Radio

	mu       sync.Mutex
	hostOpen bool
	wpm      int
	modeReg  byte
	paused   bool
	tuning   bool

	// CW buffer and echo — supports WinKeyer pointer commands for
	// random-access writes (callsign correction). Null bytes (0x00) are
	// placeholders inserted by pointer cmd 0x03; sendLoop skips them.
	buffer   [BufferSize]byte // fixed CW buffer
	bufLen   int              // high-water mark (bytes written)
	readPos  int              // sendLoop read position
	writePos int              // next write position for bufferChar
	busy     bool
	xoff     bool
	echoDone chan struct{} // closed when sendLoop goroutine exits
	cancel   chan struct{} // close to signal sendLoop to stop

	// Parser state
	state     int
	cmdByte   byte
	adminCode byte
	params    []byte
	needBytes int

	// Serial write mutex (separate from state mutex)
	writeMu sync.Mutex
	closed  bool

	// RTTY state (FSK mode via admin 0x13)
	rttyEnabled    bool               // FSK mode active
	rttyInBracket  bool               // inside [...] text block (TX active)
	rttyBuf        []byte             // accumulates text for KY chunking
	rttyDiddle     bool               // send idle diddle (LTRS) between transmissions
	rttyAutoCRLF   bool               // auto CR/LF at end of transmission
	rttyBaudRate   int                // 0=45.45, 1=50, 2=75, 3=100 baud
	rttyDoneCancel context.CancelFunc // cancels the pending ] goroutine (PTT off)
	rttyPTTActive  bool               // TX; sent, RX; not yet sent

	// Protocol trace (verbose logging)
	trace bool
}

func NewWinkeyer(c cat.Radio) *Winkeyer {
	return &Winkeyer{
		cat: c,
		wpm: 25,
		// Mode register: serial echo ON, iambic B, no autospace
		modeReg: 0x04, // bit 2 = serial echo
	}
}

// SetWPM sets the keyer speed.
func (w *Winkeyer) SetWPM(wpm int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wpm = wpm
}

func (w *Winkeyer) OpenSerial(portName string) error {
	mode := &serial.Mode{
		BaudRate: 1200,
		DataBits: 8,
		StopBits: serial.TwoStopBits,
		Parity:   serial.NoParity,
	}
	port, err := serial.Open(portName, mode)
	if err != nil {
		return err
	}
	w.port = port
	// Set DTR high as expected by Winkeyer protocol
	port.SetDTR(true)
	port.SetRTS(false)
	log.Printf("WK: serial port %s opened (1200 8N2)", portName)
	return nil
}

func (w *Winkeyer) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	if w.port != nil {
		w.port.Close()
	}
}

// SetTrace enables or disables verbose protocol tracing.
func (w *Winkeyer) SetTrace(enabled bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.trace = enabled
	if enabled {
		log.Printf("WK trace: enabled")
	} else {
		log.Printf("WK trace: disabled")
	}
}

// Run starts the main read loop. Blocks until the serial port closes.
func (w *Winkeyer) Run() {
	buf := make([]byte, 1)
	for {
		n, err := w.port.Read(buf)
		if err != nil {
			w.mu.Lock()
			closed := w.closed
			w.mu.Unlock()
			if !closed {
				log.Printf("WK: serial read error: %v", err)
			}
			return
		}
		if n == 1 {
			w.processByte(buf[0])
		}
	}
}

func (w *Winkeyer) processByte(b byte) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.trace {
		if b >= 0x20 && b <= 0x7E {
			log.Printf("WK trace: rx 0x%02x '%c' (state=%s)", b, b, stateName(w.state))
		} else {
			log.Printf("WK trace: rx 0x%02x (state=%s)", b, stateName(w.state))
		}
	}

	switch w.state {
	case stIdle:
		w.processIdle(b)
	case stAdmin:
		w.processAdminSubcode(b)
	case stAdminParam:
		w.params = append(w.params, b)
		if len(w.params) >= w.needBytes {
			w.execAdmin()
			w.state = stIdle
		}
	case stParam:
		w.params = append(w.params, b)
		if len(w.params) >= w.needBytes {
			w.execCommand()
			// execCommand may have increased needBytes (variable-length cmd);
			// only return to idle if all bytes have been collected.
			if len(w.params) >= w.needBytes {
				w.state = stIdle
			}
		}
	}
}

func (w *Winkeyer) processIdle(b byte) {
	if b == 0x00 {
		w.state = stAdmin
		if w.trace {
			log.Printf("WK trace: state idle -> admin")
		}
		return
	}

	if b >= 0x01 && b <= 0x1F {
		// Inside RTTY brackets, CR/LF are ignored rather than treated as
		// WinKeyer commands. N1MM sends {ENTER} as 0x0D which would
		// otherwise be consumed as the Farnsworth command (eating the next
		// byte as its parameter). Sending literal CR/LF via KY also breaks
		// command formatting and disrupts chunking/txDelay.
		if w.rttyInBracket && (b == 0x0D || b == 0x0A) {
			if w.trace {
				log.Printf("WK trace: RTTY ignore CR/LF (0x%02x) inside bracket", b)
			}
			return
		}
		w.cmdByte = b
		w.params = nil
		n := paramCount(b)
		if n == 0 {
			w.execCommand()
		} else {
			w.needBytes = n
			w.state = stParam
			if w.trace {
				log.Printf("WK trace: state idle -> param (cmd=0x%02x, need %d bytes)", b, n)
			}
		}
		return
	}

	// ASCII character (0x20-0x7E)
	if b >= 0x20 && b <= 0x7E {
		if w.hostOpen {
			if w.rttyEnabled || w.rttyInBracket || b == '[' || b == ']' || b == '\\' {
				w.handleRTTYChar(b)
			} else {
				w.bufferChar(b)
			}
		}
		return
	}
}

func (w *Winkeyer) processAdminSubcode(b byte) {
	w.adminCode = b
	w.params = nil
	n := adminParamCount(b)
	if n == 0 {
		w.execAdmin()
		w.state = stIdle
	} else {
		w.needBytes = n
		w.state = stAdminParam
		if w.trace {
			log.Printf("WK trace: state admin -> admin-param (sub=0x%02x, need %d bytes)", b, n)
		}
	}
}

func (w *Winkeyer) execAdmin() {
	if w.trace {
		log.Printf("WK trace: admin 0x%02x", w.adminCode)
	}
	switch w.adminCode {
	case 0x01: // Reset
		w.hostOpen = false
		wasInBracket := w.rttyInBracket
		w.rttyEnabled = false
		w.rttyInBracket = false
		w.rttyBuf = nil
		w.rttyPTTActive = false
		if w.rttyDoneCancel != nil {
			w.rttyDoneCancel()
			w.rttyDoneCancel = nil
		}
		w.clearBuffer()
		if wasInBracket {
			go w.cat.SetPTT(false)
		}
		log.Printf("WK: admin reset")

	case 0x02: // Host Open
		w.hostOpen = true
		w.clearBuffer()
		w.writeByte(byte(WKVersion))
		log.Printf("WK: host open → version %d", WKVersion)

	case 0x03: // Host Close
		w.hostOpen = false
		wasInBracket := w.rttyInBracket
		w.rttyEnabled = false
		w.rttyInBracket = false
		w.rttyBuf = nil
		w.rttyPTTActive = false
		if w.rttyDoneCancel != nil {
			w.rttyDoneCancel()
			w.rttyDoneCancel = nil
		}
		w.clearBuffer()
		if wasInBracket {
			go w.cat.SetPTT(false)
		}
		log.Printf("WK: host close")

	case 0x04: // Echo Test
		if len(w.params) > 0 {
			w.writeByte(w.params[0])
		}

	case 0x05, 0x06, 0x07: // Paddle/Speed A2D, Get Values (legacy)
		w.writeByte(0)

	case 0x09: // Get FW Major
		w.writeByte(byte(WKVersion))

	case 0x0A: // Set WK1 mode
		log.Printf("WK: set WK1 mode")

	case 0x0B: // Set WK2 mode
		log.Printf("WK: set WK2 mode")

	case 0x0C: // Dump EEPROM — send 256 zeros
		zeros := make([]byte, 256)
		w.writeBytes(zeros)

	case 0x0D: // Load EEPROM — already consumed 256 bytes, ignore
		log.Printf("WK: load EEPROM (ignored)")

	case 0x0F: // Load X1MODE
		log.Printf("WK: load X1MODE: 0x%02x", w.params[0])

	case 0x11: // Set low baud (1200)
		log.Printf("WK: set 1200 baud")

	case 0x12: // Set high baud (9600)
		log.Printf("WK: set 9600 baud (not supported)")

	case 0x13: // FSK mode — controls WinKeyer FSK hardware output, NOT radio mode.
		// N1MM manages the radio mode itself; sending MD3/MD6 here fights it.
		if len(w.params) >= 2 {
			p1 := w.params[0]
			w.rttyEnabled = p1&0x80 != 0
			w.rttyDiddle = p1&0x40 != 0
			w.rttyAutoCRLF = p1&0x10 != 0
			w.rttyBaudRate = int(p1 & 0x03)
			if w.rttyEnabled {
				log.Printf("WK: FSK mode ON (diddle=%v autoCRLF=%v baud=%d)", w.rttyDiddle, w.rttyAutoCRLF, w.rttyBaudRate)
			} else {
				log.Printf("WK: FSK mode OFF")
				w.rttyInBracket = false
				w.rttyBuf = nil
			}
		}

	case 0x17: // Get FW Minor
		w.writeByte(0)

	default:
		log.Printf("WK: admin subcode 0x%02x (ignored)", w.adminCode)
	}
}

func (w *Winkeyer) execCommand() {
	if w.trace {
		log.Printf("WK trace: cmd 0x%02x", w.cmdByte)
	}
	switch w.cmdByte {
	case 0x01: // Sidetone
		// Ignored — K4 handles sidetone

	case 0x02: // Set Speed
		wpm := int(w.params[0])
		if wpm >= 5 && wpm <= 99 {
			w.wpm = wpm
			log.Printf("WK: speed = %d WPM", wpm)
			go w.cat.SetSpeed(wpm)
		}

	case 0x03: // Weight
		// Ignored — K4 handles weighting

	case 0x04: // PTT lead/tail
		// Ignored — K4 handles PTT timing

	case 0x05: // Speed pot range
		// Ignored — we don't have a physical pot
		log.Printf("WK: speed pot range set (ignored)")

	case 0x06: // Pause
		if len(w.params) > 0 {
			w.paused = w.params[0] != 0
			log.Printf("WK: pause = %v", w.paused)
		}

	case 0x07: // Get speed pot
		// Return speed pot byte: 0x80 | mapped value
		potVal := byte(0)
		if w.wpm >= 5 {
			potVal = byte((w.wpm - 5) * 63 / 94) // Map 5-99 WPM to 0-63
		}
		w.writeByte(0x80 | (potVal & 0x3F))

	case 0x08: // Backspace
		if w.writePos > w.readPos {
			w.writePos--
			w.updateXoff()
		}

	case 0x09: // Pin config
		// Ignored

	case 0x0A: // Clear buffer
		wasBusy := w.busy
		wasRTTYBracket := w.rttyInBracket
		// Cancel any pending ] goroutine (waiting on txDelay before RX)
		hadPendingRTTY := w.rttyDoneCancel != nil
		if w.rttyDoneCancel != nil {
			w.rttyDoneCancel()
			w.rttyDoneCancel = nil
		}
		w.clearBuffer()
		log.Printf("WK: clear buffer")
		if wasRTTYBracket || hadPendingRTTY {
			w.cat.SendAbort()
			w.cat.SetPTT(false)
			w.rttyPTTActive = false
		} else if wasBusy {
			w.cat.SendAbort()
		}

	case 0x0B: // Key immediate (tune)
		if len(w.params) > 0 {
			w.tuning = w.params[0] != 0
			log.Printf("WK: key immediate = %v", w.tuning)
			go w.cat.SetPTT(w.tuning)
			w.sendStatusLocked()
		}

	case 0x0C: // HSCW
		// Ignored

	case 0x0D: // Farnsworth
		// Ignored — could be implemented but K4 doesn't support it

	case 0x0E: // Mode register
		if len(w.params) > 0 {
			w.modeReg = w.params[0]
			log.Printf("WK: mode register = 0x%02x (echo=%v)", w.modeReg, w.modeReg&0x04 != 0)
		}

	case 0x0F: // Load defaults (15 bytes)
		if len(w.params) >= 15 {
			w.modeReg = w.params[0]
			if w.params[1] >= 5 && w.params[1] <= 99 {
				w.wpm = int(w.params[1])
				go w.cat.SetSpeed(w.wpm)
			}
			log.Printf("WK: load defaults: mode=0x%02x speed=%d", w.modeReg, w.wpm)
		}

	case 0x10, 0x11, 0x12: // Extension, compensation, switchpoint
		// Ignored

	case 0x13: // Null
		// No-op

	case 0x14: // Software paddle
		// Ignored — no paddle emulation

	case 0x15: // Request status
		w.sendStatusLocked()

	case 0x16: // Pointer command (variable-length)
		sub := w.params[0]
		if sub != 0x00 && len(w.params) == 1 {
			// Subcmds 01 (overwrite), 02 (append), 03 (add nulls) need one more byte
			w.needBytes = 2
			return
		}
		switch sub {
		case 0x00: // Reset buffer pointer to start
			w.writePos = 0
		case 0x01: // Move pointer to position (overwrite mode)
			pos := int(w.params[1])
			if pos < BufferSize {
				w.writePos = pos
			}
		case 0x02: // Move pointer to position (append mode)
			pos := int(w.params[1])
			if pos < BufferSize {
				w.writePos = pos
			}
		case 0x03: // Add nulls (create space for corrections)
			count := int(w.params[1])
			end := w.writePos + count
			if end > BufferSize {
				end = BufferSize
			}
			for i := w.writePos; i < end; i++ {
				w.buffer[i] = 0
			}
			w.writePos = end
			if end > w.bufLen {
				w.bufLen = end
			}
		}
		if w.trace {
			if len(w.params) == 1 {
				log.Printf("WK trace: pointer sub=0x%02x writePos=%d", sub, w.writePos)
			} else {
				log.Printf("WK trace: pointer sub=0x%02x param=0x%02x writePos=%d", sub, w.params[1], w.writePos)
			}
		}

	case 0x17: // Dit/dah ratio
		// Ignored

	// Buffered commands (0x18-0x1F)
	case 0x18: // Buffered PTT
		if len(w.params) > 0 {
			on := w.params[0] != 0
			go w.cat.SetPTT(on)
		}

	case 0x19: // Buffered key (tune for N seconds)
		// Ignored for now

	case 0x1A: // Buffered wait
		// Could insert a pause in the echo stream

	case 0x1B: // Merge (prosign)
		if len(w.params) >= 2 {
			// Send both chars without inter-character gap
			// For our purposes, just buffer them normally
			w.bufferChar(w.params[0])
			w.bufferChar(w.params[1])
		}

	case 0x1C: // Buffered speed change
		if len(w.params) > 0 && w.params[0] >= 5 && w.params[0] <= 99 {
			w.wpm = int(w.params[0])
			go w.cat.SetSpeed(w.wpm)
		}

	case 0x1D: // Buffered HSCW
		// Ignored

	case 0x1E: // Cancel buffered speed
		// Revert to default speed — no-op for now

	case 0x1F: // Buffered NOP
		// No-op

	default:
		log.Printf("WK: unknown command 0x%02x", w.cmdByte)
	}
}

// bufferChar adds a character to the CW buffer and triggers sending.
// Writes at the current writePos (set by pointer commands).
// Must be called with w.mu held.
func (w *Winkeyer) bufferChar(ch byte) {
	ch = byte(unicode.ToUpper(rune(ch)))

	if w.writePos >= BufferSize {
		return // Buffer full, drop character
	}

	w.buffer[w.writePos] = ch
	w.writePos++
	if w.writePos > w.bufLen {
		w.bufLen = w.writePos
	}
	if w.trace {
		log.Printf("WK trace: buffer '%c' @%d (len=%d)", ch, w.writePos-1, w.bufLen)
	}
	wasBusy := w.busy
	w.busy = true
	w.updateXoff()

	if !wasBusy {
		w.sendStatusLocked()
		// Start the echo/send goroutine with its own done/cancel channels
		w.echoDone = make(chan struct{})
		w.cancel = make(chan struct{})
		go w.sendLoop(w.echoDone, w.cancel)
	}
}

// sendLoop drains the buffer, sends KY commands, and echoes characters.
// Runs in its own goroutine. done is closed on exit; cancel is monitored for abort.
// Null bytes (0x00) in the buffer are placeholders from pointer commands and are skipped.
//
// PTT is NOT managed here — the K4's CW break-in handles TX/RX automatically
// when KY commands are sent. Explicit PTT is only used for RTTY (handleRTTYChar).
func (w *Winkeyer) sendLoop(done, cancel chan struct{}) {
	defer close(done)

	// Coalescing delay — at 1200 baud bytes arrive every ~9ms; a 12-char
	// CQ message takes ~110ms. Wait long enough to capture the full
	// message in one KY chunk — splitting into multiple KY commands
	// causes audible gaps because the K4 drops out of TX between them.
	select {
	case <-time.After(120 * time.Millisecond):
	case <-cancel:
		return
	}

	for {
		select {
		case <-cancel:
			return
		default:
		}

		w.mu.Lock()
		// Re-check cancel under the lock — if clearBuffer closed the
		// channel while we were blocked on mu.Lock, exit without
		// touching the buffer (prevents stale goroutine from stealing
		// characters meant for a new sendLoop).
		select {
		case <-cancel:
			w.mu.Unlock()
			return
		default:
		}
		if w.paused {
			w.busy = false
			w.xoff = false
			w.sendStatusLocked()
			w.mu.Unlock()
			return
		}

		// Build a chunk from the buffer, stopping at null placeholders
		// (reserved space for corrections). No word-boundary splitting —
		// larger chunks reduce inter-KY gaps that cause extra spacing.
		var chunk []byte
		for w.readPos < w.bufLen && len(chunk) < KYChunkSize {
			ch := w.buffer[w.readPos]
			if ch == 0 {
				break // stop at null — don't advance past it
			}
			w.readPos++
			chunk = append(chunk, ch)
		}

		if len(chunk) == 0 {
			// Skip consecutive null placeholders (reserved space from
			// pointer cmd 0x03). Corrections should already be written
			// by now — the echo timing of the previous chunk provides
			// sufficient delay for the host to fill positions via
			// pointer commands.
			skipped := false
			for w.readPos < w.bufLen && w.buffer[w.readPos] == 0 {
				w.readPos++
				skipped = true
			}
			if skipped {
				if w.trace {
					log.Printf("WK trace: skipped nulls, readPos=%d", w.readPos)
				}
				w.mu.Unlock()
				continue
			}
			// Buffer truly empty
			w.busy = false
			w.xoff = false
			w.sendStatusLocked()
			w.mu.Unlock()
			return
		}

		wpm := w.wpm
		echoEnabled := w.modeReg&0x04 != 0
		w.updateXoff()
		w.mu.Unlock()

		// Check cancel before sending — abort should prevent new KY
		select {
		case <-cancel:
			return
		default:
		}

		// Send KY command to K4 — K4 break-in handles PTT automatically
		w.cat.SendKY(string(chunk))
		if w.trace {
			log.Printf("WK trace: KY \"%s\" (%d chars)", string(chunk), len(chunk))
		}

		// Echo characters back at WPM pace
		for _, ch := range chunk {
			dur := charDuration(ch, wpm)
			select {
			case <-time.After(dur):
			case <-cancel:
				return
			}
			if echoEnabled {
				w.writeByte(ch)
				if w.trace {
					log.Printf("WK trace: tx echo '%c'", ch)
				}
			}
		}
	}
}

// clearBuffer aborts CW and resets buffer state.
// Must be called with w.mu held.
func (w *Winkeyer) clearBuffer() {
	if w.trace {
		log.Printf("WK trace: clear (was %d chars)", w.bufLen-w.readPos)
	}
	w.bufLen = 0
	w.readPos = 0
	w.writePos = 0
	w.busy = false
	w.xoff = false
	w.paused = false
	// Signal any running sendLoop to stop
	if w.cancel != nil {
		close(w.cancel)
		w.cancel = nil
	}
	// Clear RTTY state
	if w.rttyDoneCancel != nil {
		w.rttyDoneCancel()
		w.rttyDoneCancel = nil
	}
	if w.rttyInBracket {
		w.rttyInBracket = false
		w.rttyBuf = nil
		// PTT off handled by caller (execCommand 0x0A or execAdmin reset)
	}
	w.rttyBuf = nil
	w.sendStatusLocked()
}

// handleRTTYChar processes a character in RTTY/FSK mode.
// Bracket delimiters control TX state; text inside brackets is sent via KY.
// Must be called with w.mu held.
func (w *Winkeyer) handleRTTYChar(b byte) {
	switch b {
	case '[': // Begin TX — cancel any pending PTT-off from a previous ], then key up
		if w.rttyDoneCancel != nil {
			w.rttyDoneCancel()
			w.rttyDoneCancel = nil
		}
		w.rttyBuf = w.rttyBuf[:0]
		w.rttyInBracket = true
		if !w.rttyPTTActive {
			w.cat.SetPTT(true)
			w.rttyPTTActive = true
		}
		if w.trace {
			log.Printf("WK trace: RTTY [ → PTT on (was=%v)", w.rttyPTTActive)
		}

	case ']': // End TX — flush text, delayed PTT off (cancellable by next [)
		if w.rttyInBracket {
			text := w.rttyTakeBuf()
			w.rttyInBracket = false
			txDelay := w.rttyTxDelay(len(text))
			if w.trace {
				log.Printf("WK trace: RTTY ] → flush %q + PTT off (wait %v)", text, txDelay)
			}
			// Send remaining text synchronously for correct wire ordering
			if text != "" {
				w.cat.SendRTTYText(text)
			}
			// Goroutine: coalescing delay → txDelay → RX
			// If next [ arrives within 30ms, ctx is cancelled → skip RX (merge)
			// No KY |; (EOT) — we control PTT with RX; after the Baudot delay.
			// Sending EOT too early causes the K4 to drop TX prematurely.
			ctx, cancel := context.WithCancel(context.Background())
			w.rttyDoneCancel = cancel
			go func() {
				select {
				case <-time.After(30 * time.Millisecond):
				case <-ctx.Done():
					return
				}
				select {
				case <-time.After(txDelay):
					w.cat.SetPTT(false)
					w.mu.Lock()
					w.rttyPTTActive = false
					w.mu.Unlock()
				case <-ctx.Done():
				}
			}()
		}

	case '\\': // Abort
		w.rttyBuf = w.rttyBuf[:0]
		if w.rttyInBracket {
			w.rttyInBracket = false
			w.cat.SetPTT(false)
			w.rttyPTTActive = false
			if w.trace {
				log.Printf("WK trace: RTTY \\ → abort, PTT off")
			}
		}

	case '}': // CR/LF
		if w.rttyInBracket {
			w.rttyBuf = append(w.rttyBuf, '\r', '\n')
		}

	case '{': // RYRYRY test pattern
		if w.rttyInBracket {
			w.rttyBuf = append(w.rttyBuf, []byte("RYRYRYRYRY")...)
		}

	default:
		if w.rttyInBracket {
			w.rttyBuf = append(w.rttyBuf, byte(unicode.ToUpper(rune(b))))
		} else {
			// Outside brackets — pass through to CW buffer
			w.bufferChar(b)
		}
	}

	// Echo the character back if serial echo is enabled
	if w.modeReg&0x04 != 0 {
		w.writeByte(b)
	}
}

// rttyTakeBuf returns the accumulated RTTY text (uppercased) and clears the buffer.
// Must be called with w.mu held.
func (w *Winkeyer) rttyTakeBuf() string {
	if len(w.rttyBuf) == 0 {
		return ""
	}
	text := strings.ToUpper(string(w.rttyBuf))
	w.rttyBuf = w.rttyBuf[:0]
	return text
}

// rttyTxDelay estimates how long the K4 needs to transmit n characters of RTTY.
// Uses 9 bits per character to account for 7.5-bit Baudot frames (1 start +
// 5 data + 1.5 stop) plus FIGS/LTRS shift characters that get inserted when
// switching between letters and figures (callsigns with numbers, exchanges).
// Adds 1 second padding for K4 internal buffering, EOT, and TX/RX switching.
func (w *Winkeyer) rttyTxDelay(n int) time.Duration {
	if n <= 0 {
		return 500 * time.Millisecond
	}
	// Baud rates: 0=45.45, 1=50, 2=75, 3=100
	baud := 45.45
	switch w.rttyBaudRate {
	case 1:
		baud = 50
	case 2:
		baud = 75
	case 3:
		baud = 100
	}
	msPerChar := 9000.0 / baud // ~9 bits accounts for shifts
	return time.Duration(float64(n)*msPerChar+1000) * time.Millisecond
}

func (w *Winkeyer) updateXoff() {
	remaining := w.bufLen - w.readPos
	if remaining >= XoffThreshold {
		w.xoff = true
	} else if remaining < XonThreshold {
		w.xoff = false
	}
}

// sendStatusLocked sends a status byte. Must be called with w.mu held.
func (w *Winkeyer) sendStatusLocked() {
	status := byte(0xC0)
	if w.busy {
		status |= StatusBusy
	}
	if w.xoff {
		status |= StatusXoff
	}
	if w.tuning {
		status |= StatusKeyDown
	}
	if w.trace {
		log.Printf("WK trace: tx status 0x%02x", status)
	}
	w.writeByte(status)
}

// IsRTTY returns whether RTTY/FSK mode is currently active.
func (w *Winkeyer) IsRTTY() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rttyEnabled
}

// SendStatus sends a status byte (acquires lock).
func (w *Winkeyer) SendStatus() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sendStatusLocked()
}

func (w *Winkeyer) writeByte(b byte) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if w.port != nil {
		w.port.Write([]byte{b})
	}
}

func (w *Winkeyer) writeBytes(data []byte) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if w.port != nil {
		w.port.Write(data)
	}
}

// paramCount returns the number of parameter bytes expected for a command.
func paramCount(cmd byte) int {
	switch cmd {
	case 0x01: // Sidetone
		return 1
	case 0x02: // Speed
		return 1
	case 0x03: // Weight
		return 1
	case 0x04: // PTT lead/tail
		return 2
	case 0x05: // Speed pot range
		return 3
	case 0x06: // Pause
		return 1
	case 0x07: // Get speed pot
		return 0
	case 0x08: // Backspace
		return 0
	case 0x09: // Pin config
		return 1
	case 0x0A: // Clear buffer
		return 0
	case 0x0B: // Key immediate
		return 1
	case 0x0C: // HSCW
		return 1
	case 0x0D: // Farnsworth
		return 1
	case 0x0E: // Mode register
		return 1
	case 0x0F: // Load defaults
		return 15
	case 0x10: // Extension
		return 1
	case 0x11: // Compensation
		return 1
	case 0x12: // Switchpoint
		return 1
	case 0x13: // Null
		return 0
	case 0x14: // Software paddle
		return 1
	case 0x15: // Request status
		return 0
	case 0x16: // Pointer (variable: nn=00 needs 1, others need 2)
		return 1
	case 0x17: // Ratio
		return 1
	case 0x18: // Buffered PTT
		return 1
	case 0x19: // Buffered key
		return 1
	case 0x1A: // Buffered wait
		return 1
	case 0x1B: // Buffered merge
		return 2
	case 0x1C: // Buffered speed
		return 1
	case 0x1D: // Buffered HSCW
		return 1
	case 0x1E: // Cancel buffered speed
		return 0
	case 0x1F: // Buffered NOP
		return 0
	default:
		return 0
	}
}

// adminParamCount returns additional bytes needed after the admin subcode.
func adminParamCount(subcode byte) int {
	switch subcode {
	case 0x00: // Calibrate
		return 1
	case 0x04: // Echo test
		return 1
	case 0x0D: // Load EEPROM
		return 256
	case 0x0E: // Send message
		return 1
	case 0x0F: // Load X1MODE
		return 1
	case 0x13: // FSK mode
		return 2
	case 0x15: // Load X2MODE (WK3)
		return 1
	case 0x18: // Get Paddle A2D extended
		return 3
	case 0x1C: // Set WK mode extended (WK3.1)
		return 1
	default:
		return 0
	}
}

func stateName(s int) string {
	switch s {
	case stIdle:
		return "idle"
	case stAdmin:
		return "admin"
	case stAdminParam:
		return "admin-param"
	case stParam:
		return "param"
	default:
		return "?"
	}
}
