package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// CATClient manages a TCP connection to a CAT server (QK4 or K4 direct).
// It translates Winkeyer actions into K4 CAT commands.
type CATClient struct {
	addr string
	conn net.Conn
	mu   sync.Mutex

	// Response channel for synchronous queries
	respCh chan string
}

func NewCATClient(addr string) *CATClient {
	return &CATClient{
		addr:   addr,
		respCh: make(chan string, 8),
	}
}

func (c *CATClient) Connect() error {
	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("CAT connect: %w", err)
	}
	c.conn = conn
	go c.readLoop()
	log.Printf("CAT: connected to %s", c.addr)
	return nil
}

func (c *CATClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *CATClient) readLoop() {
	scanner := bufio.NewScanner(c.conn)
	scanner.Split(splitSemicolon)
	for scanner.Scan() {
		resp := scanner.Text()
		if resp == "" {
			continue
		}
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
	if c.conn == nil {
		return fmt.Errorf("CAT: not connected")
	}
	_, err := fmt.Fprint(c.conn, cmd)
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

// SendAbort stops CW keying and clears the K4 buffer.
func (c *CATClient) SendAbort() error {
	log.Printf("CAT: KY0; (abort)")
	return c.send("KY0;")
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

// SetVolume sets the K4 audio gain (0-255).
func (c *CATClient) SetVolume(level int) error {
	if level < 0 {
		level = 0
	}
	if level > 255 {
		level = 255
	}
	cmd := fmt.Sprintf("AG%03d;", level)
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
