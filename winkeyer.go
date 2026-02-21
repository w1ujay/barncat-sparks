package main

import (
	"log"
	"sync"
	"time"
	"unicode"

	"go.bug.st/serial"
)

const (
	WKVersion = 31 // Report as WK3.1

	BufferSize    = 64
	XoffThreshold = 42 // Set XOFF when buffer >= 2/3 full
	XonThreshold  = 20 // Clear XOFF when buffer drops below this
	KYChunkSize   = 12 // Max chars per KY command (safety limit; word-split keeps chunks small)

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
	cat  *CATClient

	mu       sync.Mutex
	hostOpen bool
	wpm      int
	modeReg  byte
	paused   bool
	tuning   bool

	// CW buffer and echo
	buffer   []byte        // chars waiting to be sent/echoed
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

	// Protocol trace (verbose logging)
	trace bool
}

func NewWinkeyer(cat *CATClient) *Winkeyer {
	return &Winkeyer{
		cat: cat,
		wpm: 25,
		// Mode register: serial echo ON, iambic B, no autospace
		modeReg: 0x04, // bit 2 = serial echo
	}
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
			w.state = stIdle
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

	// ASCII character (0x20-0x7E) — buffer for CW sending
	if b >= 0x20 && b <= 0x7E {
		if w.hostOpen {
			w.bufferChar(b)
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
		w.clearBuffer()
		log.Printf("WK: admin reset")

	case 0x02: // Host Open
		w.hostOpen = true
		w.clearBuffer()
		w.writeByte(byte(WKVersion))
		log.Printf("WK: host open → version %d", WKVersion)

	case 0x03: // Host Close
		w.hostOpen = false
		w.clearBuffer()
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

	case 0x13: // FSK mode
		log.Printf("WK: FSK mode (ignored)")

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
		if len(w.buffer) > 0 {
			w.buffer = w.buffer[:len(w.buffer)-1]
			w.updateXoff()
		}

	case 0x09: // Pin config
		// Ignored

	case 0x0A: // Clear buffer
		wasBusy := w.busy
		w.clearBuffer()
		log.Printf("WK: clear buffer")
		if wasBusy {
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

	case 0x16: // Pointer command
		// Ignored

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
// Must be called with w.mu held.
func (w *Winkeyer) bufferChar(ch byte) {
	ch = byte(unicode.ToUpper(rune(ch)))

	if len(w.buffer) >= BufferSize {
		return // Buffer full, drop character
	}

	w.buffer = append(w.buffer, ch)
	if w.trace {
		log.Printf("WK trace: buffer '%c' (%d/%d)", ch, len(w.buffer), BufferSize)
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
func (w *Winkeyer) sendLoop(done, cancel chan struct{}) {
	defer close(done)

	// Brief coalescing delay — at 1200 baud bytes arrive every ~9ms,
	// wait to accumulate a few chars before sending the first chunk.
	select {
	case <-time.After(20 * time.Millisecond):
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
		if len(w.buffer) == 0 || w.paused {
			w.busy = false
			w.xoff = false
			w.sendStatusLocked()
			w.mu.Unlock()
			return
		}

		// Take a chunk for KY command, splitting at word boundaries
		// so any inter-chunk timing jitter falls on natural word spaces.
		n := len(w.buffer)
		if n > KYChunkSize {
			n = KYChunkSize
		}
		for i := 0; i < n; i++ {
			if w.buffer[i] == ' ' {
				n = i + 1 // include the trailing space
				break
			}
		}
		chunk := make([]byte, n)
		copy(chunk, w.buffer[:n])
		w.buffer = w.buffer[n:]
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

		// Send KY command to K4
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
		log.Printf("WK trace: clear (was %d chars)", len(w.buffer))
	}
	w.buffer = nil
	w.busy = false
	w.xoff = false
	w.paused = false
	// Signal any running sendLoop to stop
	if w.cancel != nil {
		close(w.cancel)
		w.cancel = nil
	}
	w.sendStatusLocked()
}

func (w *Winkeyer) updateXoff() {
	if len(w.buffer) >= XoffThreshold {
		w.xoff = true
	} else if len(w.buffer) < XonThreshold {
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
	case 0x16: // Pointer
		return 3
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
