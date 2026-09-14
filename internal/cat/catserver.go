package cat

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"
)

// CATServer accepts TCP connections and relays CAT commands to/from a CATClient.
// This allows N1MM, a remote BarnCat Sparks instance, or multiple clients to connect to a radio
// over the network. Supports multiple simultaneous clients — commands from any
// client go to the radio, responses are broadcast to all clients.
type CATServer struct {
	cat      *CATClient
	listener net.Listener
	mu       sync.Mutex
	clients  map[net.Conn]struct{}
	done     chan struct{}
}

// NewCATServer creates a CATServer listening on the given address.
func NewCATServer(cat *CATClient, listenAddr string) (*CATServer, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("CATServer listen: %w", err)
	}
	return &CATServer{
		cat:      cat,
		listener: ln,
		clients:  make(map[net.Conn]struct{}),
		done:     make(chan struct{}),
	}, nil
}

// Addr returns the listener's address (useful when using port 0 in tests).
func (s *CATServer) Addr() string {
	return s.listener.Addr().String()
}

// Run accepts TCP connections and relays commands. Blocks until Close is called.
func (s *CATServer) Run() {
	log.Printf("CATServer: listening on %s", s.listener.Addr())
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("CATServer: accept error: %v", err)
				continue
			}
		}
		log.Printf("CATServer: client connected from %s", conn.RemoteAddr())
		go s.handleClient(conn)
	}
}

// handleClient relays commands from a TCP client to the radio via CATClient.
func (s *CATServer) handleClient(conn net.Conn) {
	s.mu.Lock()
	if s.clients == nil { // accepted just before Close
		s.mu.Unlock()
		conn.Close()
		return
	}
	s.clients[conn] = struct{}{}
	s.mu.Unlock()

	// Add relay so radio responses go to this client
	s.cat.AddRelay(conn)
	defer func() {
		s.cat.RemoveRelay(conn)
		conn.Close()
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		log.Printf("CATServer: client disconnected")
	}()

	// Read commands from TCP client and forward to radio
	scanner := bufio.NewScanner(conn)
	scanner.Split(SplitSemicolon)
	for scanner.Scan() {
		cmd := scanner.Text()
		if cmd == "" {
			continue
		}
		log.Printf("CATServer: relay %s;", cmd)
		if err := s.cat.Send(cmd + ";"); err != nil {
			log.Printf("CATServer: send error: %v", err)
			return
		}
	}
}

// Close shuts down the CATServer and disconnects all clients.
func (s *CATServer) Close() {
	close(s.done)
	s.listener.Close()
	s.mu.Lock()
	for conn := range s.clients {
		conn.Close()
	}
	s.clients = nil
	s.mu.Unlock()
}
