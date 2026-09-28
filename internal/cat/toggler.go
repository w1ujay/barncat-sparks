package cat

import (
	"log"
	"sync"
	"time"
)

// toggleFilter names a K3 on/off function that has no CAT set command, only
// a front-panel tap (SWT) that flips it.
type toggleFilter int

const (
	filterNR toggleFilter = iota
	filterNotch
)

func (f toggleFilter) String() string {
	if f == filterNR {
		return "NR"
	}
	return "notch"
}

// toggler reconciles NR and notch toward the requested state. Set records
// the request and returns; one worker reads the radio's state, taps only
// when it differs, and after a tap waits for the effect (or the settle time)
// before tapping that filter again. RadioServer handles a connection's
// commands one at a time, so a setter that waited here would delay the next
// command on that connection, such as an abort.
//
// A tap in flight is tracked apart from the latest request: until it lands
// (or its settle time passes), the observed state says nothing about whether
// a newer request is met, so the worker waits instead of judging it.
type toggler struct {
	read func() (nr, notch bool, err error)
	tap  func(f toggleFilter) error

	mu      sync.Mutex
	want    [2]*bool     // latest requested state; nil = nothing requested
	flip    [2]bool      // Toggle: request the opposite of the next observed state
	pending [2]*bool     // state the last tap should produce; nil = no tap in flight
	tapped  [2]time.Time // when the pending tap was sent
	taps    [2]int       // taps sent for the current request
	closed  bool

	wake    chan struct{}
	done    chan struct{}
	stopped chan struct{}

	settle  time.Duration // how long a tap may take to show before tapping again
	poll    time.Duration // read interval while a tap is settling
	retry   time.Duration // read interval after a failed read
	maxTaps int           // taps per request before giving up
}

func newToggler(read func() (bool, bool, error), tap func(toggleFilter) error) *toggler {
	return &toggler{
		read:    read,
		tap:     tap,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
		settle:  2 * time.Second,
		poll:    100 * time.Millisecond,
		retry:   500 * time.Millisecond,
		maxTaps: 3,
	}
}

func (t *toggler) start() {
	go t.run()
}

// Set requests filter f on or off. The latest request wins.
func (t *toggler) Set(f toggleFilter, on bool) {
	t.mu.Lock()
	v := on
	t.want[f] = &v
	t.flip[f] = false
	t.taps[f] = 0
	t.mu.Unlock()
	t.kick()
}

// Toggle requests the opposite of whatever the radio shows next.
func (t *toggler) Toggle(f toggleFilter) {
	t.mu.Lock()
	t.want[f] = nil
	t.flip[f] = true
	t.taps[f] = 0
	t.mu.Unlock()
	t.kick()
}

// ForgetPending drops taps in flight. Call it only with evidence the radio
// was reset (power cycled): a tap sent before the reset can no longer land.
// A slow reply is not such evidence.
func (t *toggler) ForgetPending() {
	t.mu.Lock()
	t.pending = [2]*bool{}
	t.mu.Unlock()
	t.kick()
}

// Close stops the worker. No tap is sent after Close returns.
func (t *toggler) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	close(t.done)
	<-t.stopped
}

// idle reports whether no request is outstanding and no tap is in flight.
func (t *toggler) idle() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for f := range 2 {
		if t.want[f] != nil || t.flip[f] || t.pending[f] != nil {
			return false
		}
	}
	return true
}

func (t *toggler) kick() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *toggler) run() {
	defer close(t.stopped)
	for {
		wait := t.pass()
		var timer <-chan time.Time
		if wait > 0 {
			timer = time.After(wait)
		}
		select {
		case <-t.done:
			return
		case <-t.wake:
		case <-timer:
		}
	}
}

// pass reads the radio once and acts on each filter. It returns how long to
// wait before the next pass, or 0 to wait for a new request.
func (t *toggler) pass() time.Duration {
	if t.idle() {
		return 0
	}
	nr, notch, err := t.read()
	if err != nil {
		return t.retry
	}
	observed := [2]bool{nr, notch}
	var next time.Duration
	for i := range 2 {
		f := toggleFilter(i)
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return 0
		}
		if p := t.pending[f]; p != nil {
			switch {
			case *p == observed[f]:
				t.pending[f] = nil // the tap landed
			case time.Since(t.tapped[f]) < t.settle:
				// Still in flight: the state can't be judged until it lands.
				t.mu.Unlock()
				next = t.poll
				continue
			default:
				log.Printf("K3: %s tap did not show within %v; treating it as lost", f, t.settle)
				t.pending[f] = nil
			}
		}
		if t.flip[f] {
			v := !observed[f]
			t.want[f] = &v
			t.flip[f] = false
		}
		w := t.want[f]
		switch {
		case w == nil:
			t.mu.Unlock()
			continue
		case *w == observed[f]:
			t.want[f] = nil
			t.mu.Unlock()
			continue
		case t.taps[f] >= t.maxTaps:
			log.Printf("K3: %s did not change after %d taps; giving up", f, t.taps[f])
			t.want[f] = nil
			t.mu.Unlock()
			continue
		}
		t.taps[f]++
		expect := !observed[f]
		t.pending[f] = &expect
		t.tapped[f] = time.Now()
		t.mu.Unlock()
		if err := t.tap(f); err != nil {
			log.Printf("K3: %s tap: %v", f, err)
		}
		next = t.poll
	}
	return next
}
