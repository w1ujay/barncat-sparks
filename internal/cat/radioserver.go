package cat

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// maxCWKYText is the most text a CW sender puts in one KY command (SendKY's
// limit). RadioServer treats longer KY text as an RTTY message.
const maxCWKYText = 24

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
	logRaw   bool // log every command passed to Radio.Raw

	ampURL     string         // RF-Kit amp base URL (empty = no proxy)
	httpMux    *http.ServeMux // routes HTTP paths to handlers
	httpServer *http.Server   // serves HTTP via chanListener
	httpLn     *chanListener  // feeds HTTP connections to httpServer
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

// SetLogRaw makes RadioServer log every command it passes to Radio.Raw, and
// the reply, to find commands a radio needs a policy for. Call before Run.
func (s *RadioServer) SetLogRaw(on bool) {
	s.logRaw = on
}

// Handle registers an HTTP handler on the RadioServer's shared mux. On the
// first call it lazily creates the mux, chanListener, and httpServer. Must be
// called before Run().
func (s *RadioServer) Handle(pattern string, handler http.Handler) {
	if s.httpMux == nil {
		s.httpMux = http.NewServeMux()
		s.httpLn = newChanListener(s.listener.Addr())
		s.httpServer = &http.Server{Handler: s.httpMux}
	}
	s.httpMux.Handle(pattern, handler)
}

// SetAmpProxy configures a reverse proxy for the RF-Kit amplifier HTTP API.
// Must be called before Run(). The amp proxy is registered as the catch-all
// "/" handler so that specific paths (e.g. /api/lp100a) registered via Handle
// take priority.
func (s *RadioServer) SetAmpProxy(ampURL string) error {
	handler, err := newAmpProxy(ampURL)
	if err != nil {
		return fmt.Errorf("amp proxy: %w", err)
	}
	s.ampURL = ampURL
	s.Handle("/", handler)
	return nil
}

// Run accepts TCP connections and dispatches commands. Blocks until Close is called.
// If an amp proxy is configured, HTTP and CAT connections are multiplexed by
// peeking at the first 4 bytes of each connection.
func (s *RadioServer) Run() {
	log.Printf("RadioServer: listening on %s", s.listener.Addr())

	if s.httpServer != nil {
		if s.ampURL != "" {
			log.Printf("RadioServer: amp proxy enabled for %s", s.ampURL)
		}
		log.Printf("RadioServer: HTTP mux enabled")
		go s.httpServer.Serve(s.httpLn)
	}

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

		if s.httpLn != nil {
			go s.routeConn(conn)
		} else {
			go s.handleClient(conn)
		}
	}
}

// routeConn peeks at the first bytes of a connection to determine whether it
// is an HTTP request or a raw CAT command, then routes accordingly.
func (s *RadioServer) routeConn(conn net.Conn) {
	// Track the connection while it is classified: peekProtocol's first read
	// has no deadline, so a client that never sends would outlive Close.
	if !s.trackConn(conn) {
		conn.Close()
		return
	}
	peek, err := peekProtocol(conn)
	s.untrackConn(conn)
	if err != nil {
		conn.Close()
		return
	}
	wrapped := &prefixConn{Conn: conn, buf: peek}

	if isHTTP(peek) {
		select {
		case s.httpLn.ch <- wrapped: // httpServer tracks it from here
		case <-s.done:
			conn.Close()
		}
	} else {
		s.handleClient(wrapped)
	}
}

// trackConn registers conn so Close disconnects it. It returns false when the
// server has already closed; the caller must then close conn itself.
func (s *RadioServer) trackConn(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients == nil { // accepted just before Close
		return false
	}
	s.clients[conn] = struct{}{}
	return true
}

func (s *RadioServer) untrackConn(conn net.Conn) {
	s.mu.Lock()
	delete(s.clients, conn)
	s.mu.Unlock()
}

// handleClient reads semicolon-delimited CAT commands from a TCP client
// and dispatches each to the Radio interface.
func (s *RadioServer) handleClient(conn net.Conn) {
	if !s.trackConn(conn) {
		conn.Close()
		return
	}

	defer func() {
		conn.Close()
		s.untrackConn(conn)
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
// calls the appropriate Radio method. A form no typed handler implements goes
// to Radio.Raw instead of being dropped. Query responses are written to conn;
// a non-nil error means the client write failed and the connection should close.
func (s *RadioServer) dispatch(cmd string, conn net.Conn) error {
	if len(cmd) < 2 {
		return nil
	}
	handled, err := s.dispatchTyped(cmd, conn)
	if handled || err != nil {
		return err
	}
	return s.dispatchRaw(cmd, conn)
}

// dispatchRaw passes cmd to Radio.Raw and writes back any reply. An error
// writes nothing: the client sees a missed poll, never an error as data.
func (s *RadioServer) dispatchRaw(cmd string, conn net.Conn) error {
	reply, err := s.radio.Raw(cmd)
	if s.logRaw {
		log.Printf("RadioServer: raw %s -> %q (err=%v)", cmd, reply, err)
	}
	if err != nil || reply == "" {
		return nil
	}
	_, err = fmt.Fprintf(conn, "%s;", reply)
	return err
}

// onOff parses the exact set forms XX0 and XX1. Anything else, such as a
// bare or sub-receiver query (NB, NB$), is not a set.
func onOff(cmd string) (on, ok bool) {
	if len(cmd) != 3 || (cmd[2] != '0' && cmd[2] != '1') {
		return false, false
	}
	return cmd[2] == '1', true
}

// dispatchTyped runs the typed handler for cmd. It reports false for any
// form it doesn't implement, so dispatch passes that form to Raw.
func (s *RadioServer) dispatchTyped(cmd string, conn net.Conn) (bool, error) {
	switch cmd[:2] {
	case "FA":
		if len(cmd) == 2 {
			hz, err := s.radio.GetFreqA()
			if err != nil {
				return true, nil
			}
			_, err = fmt.Fprintf(conn, "FA%011d;", hz)
			return true, err
		}
		hz, err := strconv.Atoi(cmd[2:])
		if err != nil {
			return false, nil
		}
		s.radio.SetFreqA(hz)

	case "FB":
		if len(cmd) == 2 {
			hz, err := s.radio.GetFreqB()
			if err != nil {
				return true, nil
			}
			_, err = fmt.Fprintf(conn, "FB%011d;", hz)
			return true, err
		}
		hz, err := strconv.Atoi(cmd[2:])
		if err != nil {
			return false, nil
		}
		s.radio.SetFreqB(hz)

	case "KS":
		if len(cmd) == 2 {
			wpm, err := s.radio.QuerySpeed()
			if err != nil {
				return true, nil
			}
			_, err = fmt.Fprintf(conn, "KS%03d;", wpm)
			return true, err
		}
		wpm, err := strconv.Atoi(cmd[2:])
		if err != nil {
			return false, nil
		}
		s.radio.SetSpeed(wpm)

	case "KY":
		if len(cmd) < 4 || cmd[2] != ' ' {
			return false, nil
		}
		text := cmd[3:]
		switch {
		case text == "|":
			s.radio.SendRTTYEOT()
		case len(text) > maxCWKYText:
			// Only RTTY puts more than 24 characters in one KY: CW senders
			// chunk to 24, and SendRTTYText sends a whole message unchunked.
			// SendKY would keep just the first 24.
			s.radio.SendRTTYText(text)
		default:
			s.radio.SendKY(text)
		}

	case "KZ":
		if len(cmd) < 3 {
			return false, nil
		}
		switch cmd[2] {
		case '.':
			s.radio.SendCWElement(true)
		case '-':
			s.radio.SendCWElement(false)
		default:
			return false, nil
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
		if len(cmd) != 3 || (cmd[2] != '0' && cmd[2] != '1') {
			return false, nil
		}
		s.radio.SelectVFO(int(cmd[2] - '0'))

	case "SW":
		if cmd != "SWT11" {
			return false, nil
		}
		s.radio.SwapVFO()

	case "AG":
		recv, digits := 0, cmd[2:]
		if strings.HasPrefix(cmd, "AG$") {
			recv, digits = 1, cmd[3:]
		}
		level, err := strconv.Atoi(digits)
		if err != nil {
			return false, nil
		}
		s.radio.SetVolume(recv, level)

	case "RG":
		switch {
		case cmd == "RG$/":
			s.radio.ToggleRFGain(1)
		case cmd == "RG/":
			s.radio.ToggleRFGain(0)
		case strings.HasPrefix(cmd, "RG$-"):
			level, err := strconv.Atoi(cmd[4:])
			if err != nil {
				return false, nil
			}
			s.radio.SetRFGain(1, level)
		case strings.HasPrefix(cmd, "RG-"):
			level, err := strconv.Atoi(cmd[3:])
			if err != nil {
				return false, nil
			}
			s.radio.SetRFGain(0, level)
		default:
			return false, nil
		}

	case "PC":
		if len(cmd) <= 3 {
			return false, nil
		}
		n, err := strconv.Atoi(cmd[2 : len(cmd)-1])
		if err != nil {
			return false, nil
		}
		switch cmd[len(cmd)-1] {
		case 'L':
			s.radio.SetPower(n)
		case 'H':
			s.radio.SetPower(n * 10)
		default:
			return false, nil
		}

	case "RT":
		on, ok := onOff(cmd)
		if !ok {
			return false, nil
		}
		s.radio.SetRIT(on)

	case "RC":
		s.radio.ClearRIT()

	case "XT":
		on, ok := onOff(cmd)
		if !ok {
			return false, nil
		}
		s.radio.SetXIT(on)

	case "NB":
		on, ok := onOff(cmd)
		if !ok {
			return false, nil
		}
		s.radio.SetNB(on)

	case "NR":
		on, ok := onOff(cmd)
		if !ok {
			return false, nil
		}
		s.radio.SetNR(on)

	case "BW":
		n, err := strconv.Atoi(cmd[2:])
		if err != nil {
			return false, nil
		}
		s.radio.SetFilterBW(n * 10)

	case "NM":
		switch {
		case cmd == "NM0" || cmd == "NM1":
			s.radio.SetNotch(cmd == "NM1")
		case len(cmd) > 4:
			hz, err := strconv.Atoi(cmd[2 : len(cmd)-1])
			if err != nil {
				return false, nil
			}
			s.radio.SetNotchFreq(hz)
		default:
			return false, nil
		}

	case "MD":
		switch cmd {
		case "MD":
			mode, err := s.radio.QueryMode()
			if err != nil {
				return true, nil
			}
			_, err = fmt.Fprintf(conn, "MD%d;", mode)
			return true, err
		case "MD3":
			s.radio.SetCWMode()
		case "MD6":
			s.radio.SetDataMode()
		default:
			return false, nil
		}

	case "TD":
		if len(cmd) < 4 || cmd[2] != '$' || cmd[3] < '0' || cmd[3] > '9' {
			return false, nil
		}
		s.radio.SetTextDecode(cmd[3] > '0')

	case "TT":
		if len(cmd) != 2 {
			return false, nil
		}
		text, err := s.radio.QueryDecode()
		if err != nil {
			return true, nil
		}
		_, err = fmt.Fprintf(conn, "TT%02d%s;", len(text), text)
		return true, err

	default:
		return false, nil
	}
	return true, nil
}

// Close shuts down the RadioServer and disconnects all clients.
func (s *RadioServer) Close() {
	close(s.done)
	s.listener.Close()
	if s.httpServer != nil {
		s.httpServer.Close()
	}
	if s.httpLn != nil {
		s.httpLn.Close()
	}
	s.mu.Lock()
	for conn := range s.clients {
		conn.Close()
	}
	s.clients = nil
	s.mu.Unlock()
}
