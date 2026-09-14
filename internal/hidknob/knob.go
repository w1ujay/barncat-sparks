package hidknob

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// transport is an open knob: Read returns one HID report per call, or
// errTimeout when nothing arrived within the transport's read timeout.
type transport interface {
	Read(buf []byte) (int, error)
	Close() error
}

// opener opens the knob. On failure it returns the state that explains why
// (not_set_up, not_connected or unsupported) with a matching error.
type opener func() (transport, State, error)

// retryInterval is how often a missing or not-set-up knob is retried.
const retryInterval = 2 * time.Second

// Knob reads the knob in a background goroutine and hands mapped gestures to
// its sink, reconnecting when the knob is unplugged and plugged back in.
type Knob struct {
	sink         Sink
	index        map[uint16]Mapping
	open         opener
	retry        time.Duration
	needsWindows bool

	mu      sync.Mutex
	state   State
	started bool
	trace   bool
	learner chan uint16 // set while a Learn is waiting for the next usage

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// New creates a knob that runs mappings on sink. If two mappings use the same
// usage, the later one wins.
func New(sink Sink, mappings []Mapping) *Knob {
	k := newKnob(sink, mappings, openDevice, retryInterval)
	k.needsWindows = true
	return k
}

func newKnob(sink Sink, mappings []Mapping, open opener, retry time.Duration) *Knob {
	index := make(map[uint16]Mapping, len(mappings))
	for _, m := range mappings {
		index[m.Usage] = m
	}
	return &Knob{
		sink:  sink,
		index: index,
		open:  open,
		retry: retry,
		state: StateNotConnected,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// Start begins reading in the background. It returns ErrUnsupported on a
// platform that can't read the knob; a missing or not-set-up knob is not an
// error, since the reader keeps retrying.
func (k *Knob) Start() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.needsWindows && !supported {
		k.state = StateUnsupported
		return ErrUnsupported
	}
	if k.started {
		return nil
	}
	k.started = true
	go k.run()
	return nil
}

// State reports the knob's current connection state.
func (k *Knob) State() State {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.state
}

// SetTrace logs reports the knob sends that aren't consumer usages.
func (k *Knob) SetTrace(on bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.trace = on
}

// Learn waits for the next usage the knob sends and returns it without running
// its mapped action. It returns ErrBusy if another Learn is waiting, and
// ctx.Err() if ctx ends first.
func (k *Knob) Learn(ctx context.Context) (uint16, error) {
	ch := make(chan uint16, 1)
	k.mu.Lock()
	if k.learner != nil {
		k.mu.Unlock()
		return 0, ErrBusy
	}
	k.learner = ch
	k.mu.Unlock()

	select {
	case u := <-ch:
		return u, nil
	case <-ctx.Done():
		k.mu.Lock()
		if k.learner == ch {
			k.learner = nil
		}
		k.mu.Unlock()
		select {
		case u := <-ch: // arrived at the same moment
			return u, nil
		default:
		}
		return 0, ctx.Err()
	}
}

// Close stops the reader and closes the knob. Safe to call more than once, and
// before Start.
func (k *Knob) Close() {
	k.closeOnce.Do(func() {
		close(k.stop)
		k.mu.Lock()
		started := k.started
		k.mu.Unlock()
		if started {
			<-k.done
		}
	})
}

func (k *Knob) setState(s State) {
	k.mu.Lock()
	changed := k.state != s
	k.state = s
	k.mu.Unlock()
	if changed {
		log.Printf("knob: %s", s)
	}
}

func (k *Knob) run() {
	defer close(k.done)
	for {
		t, st, err := k.open()
		if err != nil {
			k.setState(st)
			select {
			case <-k.stop:
				return
			case <-time.After(k.retry):
			}
			continue
		}
		k.setState(StateConnected)
		k.readLoop(t)
		t.Close()
		select {
		case <-k.stop:
			return
		default:
		}
		k.setState(StateNotConnected)
	}
}

// readLoop reads until stop or a read error other than the timeout.
func (k *Knob) readLoop(t transport) {
	buf := make([]byte, 64)
	for {
		select {
		case <-k.stop:
			return
		default:
		}
		n, err := t.Read(buf)
		if errors.Is(err, errTimeout) {
			continue
		}
		if err != nil {
			log.Printf("knob: read: %v", err)
			return
		}
		k.handleReport(buf[:n])
	}
}

func (k *Knob) handleReport(b []byte) {
	usage, ok := DecodeReport(b)
	k.mu.Lock()
	trace := k.trace
	var learner chan uint16
	if ok {
		learner = k.learner
		k.learner = nil
	}
	k.mu.Unlock()

	if !ok {
		if trace && len(b) > 0 && b[0] != consumerReportID {
			log.Printf("knob: ignored report % x", b)
		}
		return
	}
	if learner != nil {
		learner <- usage // buffered; never blocks
		return
	}
	if m, found := k.index[usage]; found {
		k.sink.Handle(eventFor(m))
	}
}

// Probe opens the knob once to report its state, then closes it. The web UI
// uses it while Sparks is stopped.
func Probe() State { return probe(openDevice) }

func probe(open opener) State {
	t, st, err := open()
	if err != nil {
		return st
	}
	t.Close()
	return StateConnected
}

// LearnOnce opens the knob, waits for one usage, and closes it. The web UI uses
// it for Learn while Sparks is stopped.
func LearnOnce(ctx context.Context) (uint16, State, error) { return learnOnce(ctx, openDevice) }

func learnOnce(ctx context.Context, open opener) (uint16, State, error) {
	t, st, err := open()
	if err != nil {
		return 0, st, err
	}
	defer t.Close()
	buf := make([]byte, 64)
	for {
		if err := ctx.Err(); err != nil {
			return 0, StateConnected, err
		}
		n, err := t.Read(buf)
		if errors.Is(err, errTimeout) {
			continue
		}
		if err != nil {
			return 0, StateNotConnected, ErrNotConnected
		}
		if u, ok := DecodeReport(buf[:n]); ok {
			return u, StateConnected, nil
		}
	}
}
