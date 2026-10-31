package midi

import (
	"context"
	"errors"
	"sync"
)

// Learn errors.
var (
	ErrBusy         = errors.New("already learning")
	ErrNotConnected = errors.New("MIDI controller not connected")
	ErrUnsupported  = errors.New("MIDI requires Windows")
)

// Learned names the control a Learn captured, in MIDIMapping's terms.
type Learned struct {
	Type    string `json:"type"`
	Channel int    `json:"channel"`
	Number  int    `json:"number"`
}

// learnedFrom turns a raw message into a Learned. Only a press or a move
// counts: note off and velocity-0 note on are the release of a button.
func learnedFrom(status, data1, data2 byte) (Learned, bool) {
	channel := int(status & 0x0F)
	switch status & 0xF0 {
	case 0x90:
		if data2 == 0 {
			return Learned{}, false
		}
		return Learned{Type: "note_on", Channel: channel, Number: int(data1)}, true
	case 0xB0:
		return Learned{Type: "cc", Channel: channel, Number: int(data1)}, true
	}
	return Learned{}, false
}

// learner hands the next control to one waiting Learn through a one-shot
// channel.
type learner struct {
	mu sync.Mutex
	ch chan Learned
}

// offer gives l to a waiting Learn and reports whether one took it.
func (x *learner) offer(l Learned) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.ch == nil {
		return false
	}
	x.ch <- l // buffered, and ch is cleared before anything else can send
	x.ch = nil
	return true
}

// wait blocks for the next offer, ctx, or stop.
func (x *learner) wait(ctx context.Context, stop <-chan struct{}) (Learned, error) {
	x.mu.Lock()
	if x.ch != nil {
		x.mu.Unlock()
		return Learned{}, ErrBusy
	}
	ch := make(chan Learned, 1)
	x.ch = ch
	x.mu.Unlock()

	defer func() {
		x.mu.Lock()
		if x.ch == ch {
			x.ch = nil
		}
		x.mu.Unlock()
	}()
	select {
	case l := <-ch:
		return l, nil
	case <-ctx.Done():
		return Learned{}, ctx.Err()
	case <-stop:
		return Learned{}, ErrNotConnected
	}
}

// Learn waits for the next press or move on the driver's open devices and
// returns it instead of dispatching it. Releases still dispatch, so a held
// PTT can always be let go.
func (d *MIDIDriver) Learn(ctx context.Context) (Learned, error) {
	return d.learn.wait(ctx, d.done)
}
