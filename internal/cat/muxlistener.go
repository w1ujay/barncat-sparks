package cat

import (
	"net"
	"time"
)

// prefixConn wraps a net.Conn and replays peeked bytes before reading from the
// real connection. Used by the protocol mux to re-inject bytes consumed during
// the HTTP/CAT detection peek.
type prefixConn struct {
	net.Conn
	buf    []byte
	offset int
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if c.offset < len(c.buf) {
		n := copy(p, c.buf[c.offset:])
		c.offset += n
		return n, nil
	}
	return c.Conn.Read(p)
}

// chanListener is a channel-based net.Listener used to feed connections from
// the protocol mux into a standard http.Server.Serve().
type chanListener struct {
	ch   chan net.Conn
	addr net.Addr
	done chan struct{}
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{
		ch:   make(chan net.Conn),
		addr: addr,
		done: make(chan struct{}),
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *chanListener) Addr() net.Addr {
	return l.addr
}

// httpMethods are the 4-byte prefixes of the HTTP methods the mux recognizes —
// all distinguishable from CAT commands, which start with 2-3 uppercase
// letters followed by digits or semicolons.
var httpMethods = []string{"GET ", "PUT ", "POST", "HEAD", "DELE", "PATC", "OPTI"}

// muxPeekTimeout bounds how long the mux waits for the rest of a possible HTTP
// method once the first bytes of a connection have arrived.
const muxPeekTimeout = 500 * time.Millisecond

// isHTTP checks whether the first bytes of a connection look like an HTTP
// request method.
func isHTTP(peek []byte) bool {
	if len(peek) < 4 {
		return false
	}
	for _, m := range httpMethods {
		if string(peek[:4]) == m {
			return true
		}
	}
	return false
}

// couldBeHTTP reports whether peek is a prefix of an HTTP method, meaning more
// bytes are needed before the connection can be classified.
func couldBeHTTP(peek []byte) bool {
	for _, m := range httpMethods {
		if len(peek) <= len(m) && m[:len(peek)] == string(peek) {
			return true
		}
	}
	return false
}

// peekProtocol reads enough of a new connection to tell HTTP from CAT. A single
// read may return fewer bytes than the method prefix, so reads continue while
// the bytes so far could still begin an HTTP method. The first read has no
// deadline — a CAT client may connect and stay silent until it has something
// to send — and a command like "FA;" routes on that first read. Later reads are
// bounded by muxPeekTimeout; on timeout or EOF the bytes so far are routed.
func peekProtocol(conn net.Conn) ([]byte, error) {
	buf := make([]byte, 4)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < len(buf) && couldBeHTTP(buf[:n]) {
		conn.SetReadDeadline(time.Now().Add(muxPeekTimeout))
		for n < len(buf) && couldBeHTTP(buf[:n]) {
			m, err := conn.Read(buf[n:])
			n += m
			if err != nil {
				break
			}
		}
		conn.SetReadDeadline(time.Time{})
	}
	return buf[:n], nil
}
