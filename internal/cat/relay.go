package cat

import (
	"errors"
	"io"
	"log"
	"sync"
	"time"
)

// relayQueue bounds how many responses may wait for one relay client, and
// relayWriteTimeout how long one write to it may block. A client that falls
// behind either limit is disconnected, so it can never stall readLoop — and
// with it queries, text decode and every other client.
const (
	relayQueue        = 256
	relayWriteTimeout = 2 * time.Second
)

var errRelayOverflow = errors.New("relay queue full")

// relay is one response subscriber, drained by its own writer goroutine.
type relay struct {
	w    io.Writer
	ch   chan string
	stop chan struct{}
	once sync.Once
}

func (r *relay) close() { r.once.Do(func() { close(r.stop) }) }

// AddRelay adds an io.Writer that receives copies of all radio responses.
// Used by CATServer to broadcast to multiple TCP clients. Writes happen on a
// per-relay goroutine; readLoop only enqueues.
func (c *CATClient) AddRelay(w io.Writer) {
	r := &relay{w: w, ch: make(chan string, relayQueue), stop: make(chan struct{})}
	c.relayMu.Lock()
	c.relays = append(c.relays, r)
	c.relayMu.Unlock()
	go c.runRelay(r)
}

// RemoveRelay removes an io.Writer from the relay list and stops its writer.
func (c *CATClient) RemoveRelay(w io.Writer) {
	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	for i, r := range c.relays {
		if r.w == w {
			c.relays = append(c.relays[:i], c.relays[i+1:]...)
			r.close()
			return
		}
	}
}

// broadcast queues resp for every relay without blocking. A relay whose queue
// is already full is dropped.
func (c *CATClient) broadcast(resp string) {
	msg := resp + ";"
	var overflow []*relay
	c.relayMu.Lock()
	for _, r := range c.relays {
		select {
		case r.ch <- msg:
		default:
			overflow = append(overflow, r)
		}
	}
	c.relayMu.Unlock()
	for _, r := range overflow {
		c.dropRelay(r, errRelayOverflow)
	}
}

// runRelay writes queued responses to one relay, each bounded by
// relayWriteTimeout when the writer supports deadlines (net.Conn does).
func (c *CATClient) runRelay(r *relay) {
	for {
		select {
		case msg := <-r.ch:
			if d, ok := r.w.(interface{ SetWriteDeadline(time.Time) error }); ok {
				d.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
			}
			if _, err := io.WriteString(r.w, msg); err != nil {
				c.dropRelay(r, err)
				return
			}
		case <-r.stop:
			return
		}
	}
}

// dropRelay removes a relay that can't keep up and closes its writer when it
// is closable, so a TCP client reconnects instead of silently missing
// responses.
func (c *CATClient) dropRelay(r *relay, reason error) {
	c.relayMu.Lock()
	for i, x := range c.relays {
		if x == r {
			c.relays = append(c.relays[:i], c.relays[i+1:]...)
			break
		}
	}
	c.relayMu.Unlock()
	r.close()
	if cl, ok := r.w.(io.Closer); ok {
		cl.Close()
	}
	log.Printf("CAT: dropped relay client: %v", reason)
}
