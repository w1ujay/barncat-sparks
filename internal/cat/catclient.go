package cat

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// CATClient manages a connection to a radio via Elecraft CAT protocol.
// The transport can be TCP (K4, remote bridge) or serial (K3, etc).
type CATClient struct {
	mu        sync.Mutex
	transport io.ReadWriteCloser
	respCh    chan string
	decodeCh  chan string // dedicated channel for TT (text decode) responses

	// Response relays — all responses are broadcast to every writer in this list
	// (used by CATServer to forward responses to multiple TCP clients).
	relayMu sync.Mutex
	relays  []io.Writer
}

// NewCATClient creates a new CATClient. Call ConnectTCP or ConnectSerial to connect.
func NewCATClient() *CATClient {
	return &CATClient{
		respCh:   make(chan string, 8),
		decodeCh: make(chan string, 8),
	}
}

// ConnectTCP connects to a CAT server over TCP (e.g., K4 or remote bridge).
func (c *CATClient) ConnectTCP(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("CAT connect: %w", err)
	}
	c.mu.Lock()
	c.transport = conn
	c.mu.Unlock()
	go c.readLoop()
	log.Printf("CAT: connected to %s (TCP)", addr)
	return nil
}

// ConnectSerial connects to a radio over a serial port (e.g., K3).
func (c *CATClient) ConnectSerial(portName string, baud int) error {
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}
	port, err := serial.Open(portName, mode)
	if err != nil {
		return fmt.Errorf("CAT serial: %w", err)
	}
	c.mu.Lock()
	c.transport = port
	c.mu.Unlock()
	go c.readLoop()
	log.Printf("CAT: connected to %s (serial, %d baud)", portName, baud)
	return nil
}

// AddRelay adds an io.Writer that receives copies of all radio responses.
// Used by CATServer to broadcast to multiple TCP clients.
func (c *CATClient) AddRelay(w io.Writer) {
	c.relayMu.Lock()
	c.relays = append(c.relays, w)
	c.relayMu.Unlock()
}

// RemoveRelay removes an io.Writer from the relay list.
func (c *CATClient) RemoveRelay(w io.Writer) {
	c.relayMu.Lock()
	for i, r := range c.relays {
		if r == w {
			c.relays = append(c.relays[:i], c.relays[i+1:]...)
			break
		}
	}
	c.relayMu.Unlock()
}

func (c *CATClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport != nil {
		c.transport.Close()
		c.transport = nil
	}
}

func (c *CATClient) readLoop() {
	c.mu.Lock()
	t := c.transport
	c.mu.Unlock()
	if t == nil {
		return
	}

	scanner := bufio.NewScanner(t)
	scanner.Split(SplitSemicolon)
	for scanner.Scan() {
		resp := scanner.Text()
		if resp == "" {
			continue
		}
		// Broadcast to all CATServer TCP clients
		c.relayMu.Lock()
		for _, w := range c.relays {
			fmt.Fprintf(w, "%s;", resp)
		}
		c.relayMu.Unlock()

		// Route TT (text decode) responses to dedicated channel
		if strings.HasPrefix(resp, "TT") {
			select {
			case c.decodeCh <- resp:
			default:
			}
			continue
		}

		select {
		case c.respCh <- resp:
		default:
			// Drop if nobody is listening
		}
	}
}

// SplitSemicolon is a bufio.SplitFunc that splits on semicolons.
func SplitSemicolon(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i := 0; i < len(data); i++ {
		if data[i] == ';' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (c *CATClient) Send(cmd string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport == nil {
		return fmt.Errorf("CAT: not connected")
	}
	_, err := fmt.Fprint(c.transport, cmd)
	if err != nil {
		log.Printf("CAT send error: %v", err)
	}
	return err
}

// SendKY sends CW text to the K4 keyer. Max ~24 chars per command.
func (c *CATClient) SendKY(text string) error {
	// K4 KY format: "KY text;" (space after KY, up to ~24 chars)
	if len(text) > 24 {
		text = text[:24]
	}
	cmd := fmt.Sprintf("KY %s;", text)
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SendAbort stops CW keying by switching the K4 to receive.
func (c *CATClient) SendAbort() error {
	log.Printf("CAT: RX; (abort)")
	return c.Send("RX;")
}

// SetSpeed sets the K4 keyer speed in WPM.
func (c *CATClient) SetSpeed(wpm int) error {
	cmd := fmt.Sprintf("KS%03d;", wpm)
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetPTT controls transmit state.
func (c *CATClient) SetPTT(on bool) error {
	cmd := "RX;"
	if on {
		cmd = "TX;"
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// drainResponses discards any stale buffered responses in respCh.
// Must be called before sending a query to ensure we read a fresh response.
func (c *CATClient) drainResponses() {
	for {
		select {
		case <-c.respCh:
		default:
			return
		}
	}
}

// QuerySpeed queries the current keyer speed from the K4.
func (c *CATClient) QuerySpeed() (int, error) {
	c.drainResponses()
	if err := c.Send("KS;"); err != nil {
		return 0, err
	}
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case resp := <-c.respCh:
			if strings.HasPrefix(resp, "KS") {
				var wpm int
				fmt.Sscanf(resp[2:], "%d", &wpm)
				return wpm, nil
			}
		case <-timeout:
			return 0, fmt.Errorf("KS query timeout")
		}
	}
}

// SetFreqA sets VFO A frequency in Hz.
func (c *CATClient) SetFreqA(hz int) error {
	cmd := fmt.Sprintf("FA%011d;", hz)
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetFreqB sets VFO B frequency in Hz.
func (c *CATClient) SetFreqB(hz int) error {
	cmd := fmt.Sprintf("FB%011d;", hz)
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// GetFreqA queries VFO A frequency from the K4. Returns Hz.
func (c *CATClient) GetFreqA() (int, error) {
	c.drainResponses()
	if err := c.Send("FA;"); err != nil {
		return 0, err
	}
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case resp := <-c.respCh:
			if strings.HasPrefix(resp, "FA") {
				var hz int
				fmt.Sscanf(resp[2:], "%d", &hz)
				return hz, nil
			}
		case <-timeout:
			return 0, fmt.Errorf("FA query timeout")
		}
	}
}

// SetVolume sets the K4 audio gain (0-60) for a receiver.
// recv: 0 = main, 1 = sub. K4 format: AGnnn; (main) or AG$nnn; (sub).
func (c *CATClient) SetVolume(recv, level int) error {
	if level < 0 {
		level = 0
	}
	if level > 60 {
		level = 60
	}
	var cmd string
	if recv == 1 {
		cmd = fmt.Sprintf("AG$%03d;", level)
	} else {
		cmd = fmt.Sprintf("AG%03d;", level)
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// ToggleRFGain toggles RF gain between full and reduced for a receiver.
// recv: 0 = main, 1 = sub. K4 format: RG/; (main) or RG$/; (sub).
func (c *CATClient) ToggleRFGain(recv int) error {
	cmd := "RG/;"
	if recv == 1 {
		cmd = "RG$/;"
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetRFGain sets the K4 RF gain attenuation (0-60 dB) for a receiver.
// recv: 0 = main, 1 = sub. K4 format: RG-nn; (main) or RG$-nn; (sub).
// level 0 = no attenuation (full gain), 60 = max attenuation.
func (c *CATClient) SetRFGain(recv, level int) error {
	if level < 0 {
		level = 0
	}
	if level > 60 {
		level = 60
	}
	var cmd string
	if recv == 1 {
		cmd = fmt.Sprintf("RG$-%02d;", level)
	} else {
		cmd = fmt.Sprintf("RG-%02d;", level)
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetPower sets the K4 transmit power using L (QRP) or H (QRO) range.
// tenths is power in 0.1W units (e.g., 50 = 5.0W, 500 = 50W).
// ≤100 (10.0W): uses L range with 0.1W resolution.
// >100: uses H range with 1W resolution.
func (c *CATClient) SetPower(tenths int) error {
	if tenths < 1 {
		tenths = 1
	}
	var cmd string
	if tenths <= 100 {
		cmd = fmt.Sprintf("PC%03dL;", tenths)
	} else {
		watts := tenths / 10
		if watts > 110 {
			watts = 110
		}
		cmd = fmt.Sprintf("PC%03dH;", watts)
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// BandUp moves to the next band.
func (c *CATClient) BandUp() error {
	log.Printf("CAT: BU;")
	return c.Send("BU;")
}

// BandDown moves to the previous band.
func (c *CATClient) BandDown() error {
	log.Printf("CAT: BD;")
	return c.Send("BD;")
}

// SelectVFO selects VFO A (0) or VFO B (1).
func (c *CATClient) SelectVFO(ab int) error {
	cmd := fmt.Sprintf("FT%d;", ab&1)
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SwapVFO swaps VFO A and B.
func (c *CATClient) SwapVFO() error {
	log.Printf("CAT: SWT11;")
	return c.Send("SWT11;")
}

// VFOAtoB copies VFO A frequency to VFO B.
func (c *CATClient) VFOAtoB() error {
	hz, err := c.GetFreqA()
	if err != nil {
		return err
	}
	return c.SetFreqB(hz)
}

// VFOBtoA copies VFO B frequency to VFO A.
func (c *CATClient) VFOBtoA() error {
	hz, err := c.GetFreqB()
	if err != nil {
		return err
	}
	return c.SetFreqA(hz)
}

// GetFreqB queries VFO B frequency from the K4. Returns Hz.
func (c *CATClient) GetFreqB() (int, error) {
	c.drainResponses()
	if err := c.Send("FB;"); err != nil {
		return 0, err
	}
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case resp := <-c.respCh:
			if strings.HasPrefix(resp, "FB") {
				var hz int
				fmt.Sscanf(resp[2:], "%d", &hz)
				return hz, nil
			}
		case <-timeout:
			return 0, fmt.Errorf("FB query timeout")
		}
	}
}

// SetRIT sets RIT on or off.
func (c *CATClient) SetRIT(on bool) error {
	cmd := "RT0;"
	if on {
		cmd = "RT1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// ClearRIT clears the RIT offset to zero.
func (c *CATClient) ClearRIT() error {
	log.Printf("CAT: RC;")
	return c.Send("RC;")
}

// SetXIT sets XIT on or off.
func (c *CATClient) SetXIT(on bool) error {
	cmd := "XT0;"
	if on {
		cmd = "XT1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetNB sets Noise Blanker on or off.
func (c *CATClient) SetNB(on bool) error {
	cmd := "NB0;"
	if on {
		cmd = "NB1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetNR sets Noise Reduction on or off.
func (c *CATClient) SetNR(on bool) error {
	cmd := "NR0;"
	if on {
		cmd = "NR1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetSplit sets Split mode on or off.
func (c *CATClient) SetSplit(on bool) error {
	cmd := "FT0;"
	if on {
		cmd = "FT1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetFilterBW sets the IF filter bandwidth in Hz. Uses main receiver.
// K4 format: BWnnnn; where nnnn is bandwidth in 10 Hz units.
func (c *CATClient) SetFilterBW(hz int) error {
	if hz < 50 {
		hz = 50
	}
	if hz > 4000 {
		hz = 4000
	}
	cmd := fmt.Sprintf("BW%04d;", hz/10)
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetNotch toggles the manual notch filter on or off. K4 format: NM/; (toggle).
func (c *CATClient) SetNotch(on bool) error {
	cmd := "NM0;"
	if on {
		cmd = "NM1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SetNotchFreq sets the manual notch frequency (150-5000 Hz) and enables it.
// K4 format: NMnnnn1; where nnnn is the notch pitch in Hz.
func (c *CATClient) SetNotchFreq(hz int) error {
	if hz < 150 {
		hz = 150
	}
	if hz > 5000 {
		hz = 5000
	}
	cmd := fmt.Sprintf("NM%04d1;", hz)
	log.Printf("CAT: %s", cmd)
	return c.Send(cmd)
}

// SendCWMessage sends a CW text message via the KY command.
func (c *CATClient) SendCWMessage(text string) error {
	// Send in chunks of up to 24 chars
	for len(text) > 0 {
		chunk := text
		if len(chunk) > 24 {
			chunk = chunk[:24]
		}
		text = text[len(chunk):]
		if err := c.SendKY(chunk); err != nil {
			return err
		}
	}
	return nil
}

// SetDataMode switches the K4 to DATA mode with FSK-D sub-mode for RTTY.
func (c *CATClient) SetDataMode() error {
	log.Printf("CAT: MD6;DT2; (DATA/FSK-D)")
	if err := c.Send("MD6;"); err != nil {
		return err
	}
	return c.Send("DT2;")
}

// SetCWMode switches the K4 back to CW mode.
func (c *CATClient) SetCWMode() error {
	log.Printf("CAT: MD3; (CW)")
	return c.Send("MD3;")
}

// SendRTTYText sends RTTY text via KY commands in 24-char chunks.
// In FSK-D mode the K4 encodes the ASCII text as Baudot.
func (c *CATClient) SendRTTYText(text string) error {
	for len(text) > 0 {
		chunk := text
		if len(chunk) > 24 {
			chunk = chunk[:24]
		}
		text = text[len(chunk):]
		cmd := fmt.Sprintf("KY %s;", chunk)
		log.Printf("CAT: %s", cmd)
		if err := c.Send(cmd); err != nil {
			return err
		}
	}
	return nil
}

// SendRTTYEOT sends the end-of-transmission marker for RTTY.
// The K4 interprets "KY |;" as a signal to finish the current transmission.
func (c *CATClient) SendRTTYEOT() error {
	log.Printf("CAT: KY |; (RTTY EOT)")
	return c.Send("KY |;")
}

// QueryMode queries the current operating mode from the radio.
// Returns the mode number: 1=LSB, 2=USB, 3=CW, 4=FM, 5=AM, 6=DATA, 7=CW-REV, 9=DATA-REV.
func (c *CATClient) QueryMode() (int, error) {
	c.drainResponses()
	if err := c.Send("MD;"); err != nil {
		return 0, err
	}
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case resp := <-c.respCh:
			if strings.HasPrefix(resp, "MD") {
				var mode int
				fmt.Sscanf(resp[2:], "%d", &mode)
				return mode, nil
			}
		case <-timeout:
			return 0, fmt.Errorf("MD query timeout")
		}
	}
}

// SetTextDecode enables or disables the K4's built-in text decoder.
// Uses TD$ command: TD$mtl; where m=mode, t=threshold, l=lines.
// Queries the radio's current operating mode to send the correct parameters:
//
//	CW/CW-REV (3,7): m=2 (RX 8-45 WPM), t=0 (auto), l=5
//	DATA/DATA-REV (6,9): m=1 (on), t=0, l=5
func (c *CATClient) SetTextDecode(on bool) error {
	if !on {
		log.Printf("CAT: TD$000; (text decode OFF)")
		return c.Send("TD$000;")
	}
	mode, err := c.QueryMode()
	if err != nil {
		log.Printf("CAT: mode query failed, defaulting to CW decode: %v", err)
		mode = 3
	}
	switch mode {
	case 6, 9: // DATA, DATA-REV
		log.Printf("CAT: TD$105; (DATA text decode ON, 5 lines)")
		return c.Send("TD$105;")
	default: // CW, CW-REV, etc.
		log.Printf("CAT: TD$205; (CW text decode ON, auto threshold, 5 lines)")
		return c.Send("TD$205;")
	}
}

// QueryDecode polls the K4 text decoder. The K4 returns TTnntext where nn is
// the character count. Returns the decoded text, or ("", nil) on timeout
// (normal when no new text is available).
func (c *CATClient) QueryDecode() (string, error) {
	if err := c.Send("TT;"); err != nil {
		return "", err
	}
	select {
	case resp := <-c.decodeCh:
		if len(resp) < 4 {
			return "", nil
		}
		n, err := strconv.Atoi(resp[2:4])
		if err != nil || n == 0 {
			return "", nil
		}
		return resp[4:], nil
	case <-time.After(500 * time.Millisecond):
		return "", nil
	}
}
