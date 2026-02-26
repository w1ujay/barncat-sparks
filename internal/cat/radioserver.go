package cat

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
)

// RadioServer is a protocol-translating TCP server that wraps any Radio
// interface and serves K4-compatible CAT over TCP. Unlike CATServer (a dumb
// byte relay tied to *CATClient), RadioServer operates at the semantic level:
// it parses incoming K4 CAT commands and calls the corresponding Radio methods.
type RadioServer struct {
	radio    Radio
	listener net.Listener
	mu       sync.Mutex
	clients  map[net.Conn]struct{}
	done     chan struct{}
}

// NewRadioServer creates a RadioServer listening on the given address.
func NewRadioServer(radio Radio, listenAddr string) (*RadioServer, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("RadioServer listen: %w", err)
	}
	return &RadioServer{
		radio:    radio,
		listener: ln,
		clients:  make(map[net.Conn]struct{}),
		done:     make(chan struct{}),
	}, nil
}

// Addr returns the listener's address (useful when using port 0 in tests).
func (s *RadioServer) Addr() string {
	return s.listener.Addr().String()
}

// Run accepts TCP connections and dispatches commands. Blocks until Close is called.
func (s *RadioServer) Run() {
	log.Printf("RadioServer: listening on %s", s.listener.Addr())
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("RadioServer: accept error: %v", err)
				continue
			}
		}
		log.Printf("RadioServer: client connected from %s", conn.RemoteAddr())
		go s.handleClient(conn)
	}
}

// handleClient reads semicolon-delimited CAT commands from a TCP client
// and dispatches each to the Radio interface.
func (s *RadioServer) handleClient(conn net.Conn) {
	s.mu.Lock()
	s.clients[conn] = struct{}{}
	s.mu.Unlock()

	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		log.Printf("RadioServer: client disconnected")
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Split(SplitSemicolon)
	for scanner.Scan() {
		cmd := scanner.Text()
		if cmd == "" {
			continue
		}
		if err := s.dispatch(cmd, conn); err != nil {
			log.Printf("RadioServer: write error: %v", err)
			return
		}
	}
}

// dispatch parses a single K4 CAT command (without trailing semicolon) and
// calls the appropriate Radio method. Query responses are written to conn;
// a non-nil error means the client write failed and the connection should close.
func (s *RadioServer) dispatch(cmd string, conn net.Conn) error {
	if len(cmd) < 2 {
		return nil
	}

	switch cmd[:2] {
	case "FA":
		if len(cmd) == 2 {
			hz, err := s.radio.GetFreqA()
			if err != nil {
				return nil
			}
			_, err = fmt.Fprintf(conn, "FA%011d;", hz)
			return err
		}
		hz, err := strconv.Atoi(cmd[2:])
		if err == nil {
			s.radio.SetFreqA(hz)
		}

	case "FB":
		if len(cmd) == 2 {
			hz, err := s.radio.GetFreqB()
			if err != nil {
				return nil
			}
			_, err = fmt.Fprintf(conn, "FB%011d;", hz)
			return err
		}
		hz, err := strconv.Atoi(cmd[2:])
		if err == nil {
			s.radio.SetFreqB(hz)
		}

	case "KS":
		if len(cmd) == 2 {
			wpm, err := s.radio.QuerySpeed()
			if err != nil {
				return nil
			}
			_, err = fmt.Fprintf(conn, "KS%03d;", wpm)
			return err
		}
		wpm, err := strconv.Atoi(cmd[2:])
		if err == nil {
			s.radio.SetSpeed(wpm)
		}

	case "KY":
		if len(cmd) < 4 || cmd[2] != ' ' {
			return nil
		}
		text := cmd[3:]
		if text == "|" {
			s.radio.SendRTTYEOT()
		} else {
			s.radio.SendKY(text)
		}

	case "TX":
		s.radio.SetPTT(true)

	case "RX":
		s.radio.SendAbort()

	case "BU":
		s.radio.BandUp()

	case "BD":
		s.radio.BandDown()

	case "FT":
		if len(cmd) > 2 {
			n := int(cmd[2] - '0')
			s.radio.SelectVFO(n)
		}

	case "SW":
		if cmd == "SWT11" {
			s.radio.SwapVFO()
		}

	case "AG":
		if len(cmd) > 2 && cmd[2] == '$' {
			level, err := strconv.Atoi(cmd[3:])
			if err == nil {
				s.radio.SetVolume(1, level)
			}
		} else if len(cmd) > 2 {
			level, err := strconv.Atoi(cmd[2:])
			if err == nil {
				s.radio.SetVolume(0, level)
			}
		}

	case "RG":
		if len(cmd) > 2 && cmd[2] == '$' {
			if len(cmd) > 3 && cmd[3] == '/' {
				s.radio.ToggleRFGain(1)
			} else if len(cmd) > 3 && cmd[3] == '-' {
				level, err := strconv.Atoi(cmd[4:])
				if err == nil {
					s.radio.SetRFGain(1, level)
				}
			}
		} else if len(cmd) > 2 && cmd[2] == '/' {
			s.radio.ToggleRFGain(0)
		} else if len(cmd) > 2 && cmd[2] == '-' {
			level, err := strconv.Atoi(cmd[3:])
			if err == nil {
				s.radio.SetRFGain(0, level)
			}
		}

	case "PC":
		if len(cmd) > 3 {
			suffix := cmd[len(cmd)-1]
			digits := cmd[2 : len(cmd)-1]
			n, err := strconv.Atoi(digits)
			if err == nil {
				switch suffix {
				case 'L':
					s.radio.SetPower(n)
				case 'H':
					s.radio.SetPower(n * 10)
				}
			}
		}

	case "RT":
		if len(cmd) > 2 {
			s.radio.SetRIT(cmd[2] == '1')
		}

	case "RC":
		s.radio.ClearRIT()

	case "XT":
		if len(cmd) > 2 {
			s.radio.SetXIT(cmd[2] == '1')
		}

	case "NB":
		if len(cmd) > 2 {
			s.radio.SetNB(cmd[2] == '1')
		}

	case "NR":
		if len(cmd) > 2 {
			s.radio.SetNR(cmd[2] == '1')
		}

	case "BW":
		if len(cmd) > 2 {
			n, err := strconv.Atoi(cmd[2:])
			if err == nil {
				s.radio.SetFilterBW(n * 10)
			}
		}

	case "NM":
		if len(cmd) == 3 {
			s.radio.SetNotch(cmd[2] == '1')
		} else if len(cmd) > 4 {
			hz, err := strconv.Atoi(cmd[2 : len(cmd)-1])
			if err == nil {
				s.radio.SetNotchFreq(hz)
			}
		}

	case "MD":
		if len(cmd) == 2 {
			mode, err := s.radio.QueryMode()
			if err != nil {
				return nil
			}
			_, err = fmt.Fprintf(conn, "MD%d;", mode)
			return err
		}
		switch cmd[2] {
		case '3':
			s.radio.SetCWMode()
		case '6':
			s.radio.SetDataMode()
		}

	case "DT":
		// no-op — SetDataMode sends MD6+DT2 internally

	case "TD":
		if len(cmd) > 3 && cmd[2] == '$' {
			s.radio.SetTextDecode(cmd[3] > '0')
		}

	case "TT":
		if len(cmd) == 2 {
			text, err := s.radio.QueryDecode()
			if err != nil {
				return nil
			}
			_, err = fmt.Fprintf(conn, "TT%02d%s;", len(text), text)
			return err
		}

	default:
		log.Printf("RadioServer: unrecognized command: %s", cmd)
	}

	return nil
}

// Close shuts down the RadioServer and disconnects all clients.
func (s *RadioServer) Close() {
	close(s.done)
	s.listener.Close()
	s.mu.Lock()
	for conn := range s.clients {
		conn.Close()
	}
	s.clients = nil
	s.mu.Unlock()
}
