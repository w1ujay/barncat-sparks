package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
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

	// Response relays — all responses are broadcast to every writer in this list
	// (used by CATServer to forward responses to multiple TCP clients).
	relayMu sync.Mutex
	relays  []io.Writer
}

// NewCATClient creates a new CATClient. Call ConnectTCP or ConnectSerial to connect.
func NewCATClient() *CATClient {
	return &CATClient{
		respCh: make(chan string, 8),
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
	scanner.Split(splitSemicolon)
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

		select {
		case c.respCh <- resp:
		default:
			// Drop if nobody is listening
		}
	}
}

// splitSemicolon is a bufio.SplitFunc that splits on semicolons.
func splitSemicolon(data []byte, atEOF bool) (advance int, token []byte, err error) {
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

func (c *CATClient) send(cmd string) error {
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
	return c.send(cmd)
}

// SendAbort stops CW keying by switching the K4 to receive.
func (c *CATClient) SendAbort() error {
	log.Printf("CAT: RX; (abort)")
	return c.send("RX;")
}

// SetSpeed sets the K4 keyer speed in WPM.
func (c *CATClient) SetSpeed(wpm int) error {
	cmd := fmt.Sprintf("KS%03d;", wpm)
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SetPTT controls transmit state.
func (c *CATClient) SetPTT(on bool) error {
	cmd := "RX;"
	if on {
		cmd = "TX;"
	}
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// QuerySpeed queries the current keyer speed from the K4.
func (c *CATClient) QuerySpeed() (int, error) {
	if err := c.send("KS;"); err != nil {
		return 0, err
	}
	// Wait for response with timeout
	select {
	case resp := <-c.respCh:
		if strings.HasPrefix(resp, "KS") {
			var wpm int
			fmt.Sscanf(resp[2:], "%d", &wpm)
			return wpm, nil
		}
	case <-time.After(500 * time.Millisecond):
	}
	return 0, fmt.Errorf("KS query timeout")
}

// SetFreqA sets VFO A frequency in Hz.
func (c *CATClient) SetFreqA(hz int) error {
	cmd := fmt.Sprintf("FA%011d;", hz)
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SetFreqB sets VFO B frequency in Hz.
func (c *CATClient) SetFreqB(hz int) error {
	cmd := fmt.Sprintf("FB%011d;", hz)
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// GetFreqA queries VFO A frequency from the K4. Returns Hz.
func (c *CATClient) GetFreqA() (int, error) {
	if err := c.send("FA;"); err != nil {
		return 0, err
	}
	select {
	case resp := <-c.respCh:
		if strings.HasPrefix(resp, "FA") {
			var hz int
			fmt.Sscanf(resp[2:], "%d", &hz)
			return hz, nil
		}
	case <-time.After(500 * time.Millisecond):
	}
	return 0, fmt.Errorf("FA query timeout")
}

// SetVolume sets the K4 audio gain (0-255) for a receiver.
// recv: 0 = main (VFO A), 1 = sub (VFO B).
func (c *CATClient) SetVolume(recv, level int) error {
	if level < 0 {
		level = 0
	}
	if level > 255 {
		level = 255
	}
	cmd := fmt.Sprintf("AG%d%03d;", recv&1, level)
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SetRFGain sets the K4 RF gain (0-250) for a receiver.
// recv: 0 = main (VFO A), 1 = sub (VFO B).
func (c *CATClient) SetRFGain(recv, level int) error {
	if level < 0 {
		level = 0
	}
	if level > 250 {
		level = 250
	}
	cmd := fmt.Sprintf("RG%d%03d;", recv&1, level)
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SetPower sets the K4 transmit power (0-110 watts).
func (c *CATClient) SetPower(watts int) error {
	if watts < 0 {
		watts = 0
	}
	if watts > 110 {
		watts = 110
	}
	cmd := fmt.Sprintf("PC%03d;", watts)
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// BandUp moves to the next band.
func (c *CATClient) BandUp() error {
	log.Printf("CAT: BU;")
	return c.send("BU;")
}

// BandDown moves to the previous band.
func (c *CATClient) BandDown() error {
	log.Printf("CAT: BD;")
	return c.send("BD;")
}

// SelectVFO selects VFO A (0) or VFO B (1).
func (c *CATClient) SelectVFO(ab int) error {
	cmd := fmt.Sprintf("FT%d;", ab&1)
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SwapVFO swaps VFO A and B.
func (c *CATClient) SwapVFO() error {
	log.Printf("CAT: SWT11;")
	return c.send("SWT11;")
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
	if err := c.send("FB;"); err != nil {
		return 0, err
	}
	select {
	case resp := <-c.respCh:
		if strings.HasPrefix(resp, "FB") {
			var hz int
			fmt.Sscanf(resp[2:], "%d", &hz)
			return hz, nil
		}
	case <-time.After(500 * time.Millisecond):
	}
	return 0, fmt.Errorf("FB query timeout")
}

// SetRIT sets RIT on or off.
func (c *CATClient) SetRIT(on bool) error {
	cmd := "RT0;"
	if on {
		cmd = "RT1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// ClearRIT clears the RIT offset to zero.
func (c *CATClient) ClearRIT() error {
	log.Printf("CAT: RC;")
	return c.send("RC;")
}

// SetXIT sets XIT on or off.
func (c *CATClient) SetXIT(on bool) error {
	cmd := "XT0;"
	if on {
		cmd = "XT1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SetNB sets Noise Blanker on or off.
func (c *CATClient) SetNB(on bool) error {
	cmd := "NB0;"
	if on {
		cmd = "NB1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SetNR sets Noise Reduction on or off.
func (c *CATClient) SetNR(on bool) error {
	cmd := "NR0;"
	if on {
		cmd = "NR1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SetSplit sets Split mode on or off.
func (c *CATClient) SetSplit(on bool) error {
	cmd := "FT0;"
	if on {
		cmd = "FT1;"
	}
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
}

// SetFilterBW sets the IF filter bandwidth in Hz. Uses main receiver (0).
func (c *CATClient) SetFilterBW(hz int) error {
	if hz < 50 {
		hz = 50
	}
	if hz > 4000 {
		hz = 4000
	}
	cmd := fmt.Sprintf("BW0%04d;", hz)
	log.Printf("CAT: %s", cmd)
	return c.send(cmd)
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
