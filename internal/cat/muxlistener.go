package cat

import (
	"net"
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

// isHTTP checks whether the first bytes of a connection look like an HTTP
// request method. HTTP methods start with "GET ", "PUT ", "POST", "HEAD",
// "DELE", "PATC", "OPTI" — all distinguishable from CAT commands which start
// with 2-3 uppercase letters followed by digits or semicolons.
func isHTTP(peek []byte) bool {
	if len(peek) < 4 {
		return false
	}
	switch string(peek[:4]) {
	case "GET ", "PUT ", "POST", "HEAD", "DELE", "PATC", "OPTI":
		return true
	}
	return false
}
