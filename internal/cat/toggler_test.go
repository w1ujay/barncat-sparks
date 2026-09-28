package cat

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// swState simulates the K3's NR/NTCH switches for toggler tests.
type swState struct {
	mu        sync.Mutex
	on        [2]bool
	taps      []toggleFilter
	readErr   error
	ignore    bool          // taps have no effect (K3 off)
	delay     time.Duration // time from a tap to its effect
	readBlock chan struct{} // when set, read waits on it
}

func (s *swState) read() (bool, bool, error) {
	s.mu.Lock()
	block := s.readBlock
	s.mu.Unlock()
	if block != nil {
		<-block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return false, false, s.readErr
	}
	return s.on[filterNR], s.on[filterNotch], nil
}

func (s *swState) tap(f toggleFilter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taps = append(s.taps, f)
	if s.ignore {
		return nil
	}
	flip := func() { s.mu.Lock(); s.on[f] = !s.on[f]; s.mu.Unlock() }
	if s.delay > 0 {
		time.AfterFunc(s.delay, flip)
	} else {
		s.on[f] = !s.on[f]
	}
	return nil
}

func (s *swState) tapCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.taps)
}

func (s *swState) get(f toggleFilter) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on[f]
}

func (s *swState) set(fn func(s *swState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func newTestToggler(t *testing.T, s *swState, settle time.Duration) *toggler {
	t.Helper()
	tg := newToggler(s.read, s.tap)
	tg.settle = settle
	tg.poll = 2 * time.Millisecond
	tg.retry = 5 * time.Millisecond
	tg.start()
	t.Cleanup(tg.Close)
	return tg
}

func TestTogglerAlreadyInStateSendsNoTap(t *testing.T) {
	s := &swState{on: [2]bool{true, false}}
	tg := newTestToggler(t, s, 50*time.Millisecond)
	tg.Set(filterNR, true)
	waitUntil(t, "idle", tg.idle)
	if n := s.tapCount(); n != 0 {
		t.Fatalf("taps = %d, want 0 (NR was already on)", n)
	}
}

func TestTogglerTapsOnDifference(t *testing.T) {
	for _, f := range []toggleFilter{filterNR, filterNotch} {
		s := &swState{on: [2]bool{true, true}}
		tg := newTestToggler(t, s, 50*time.Millisecond)
		tg.Set(f, false)
		waitUntil(t, "idle", tg.idle)
		if s.tapCount() != 1 || s.get(f) {
			t.Fatalf("filter %d: taps = %d, on = %v; want 1 tap, off", f, s.tapCount(), s.get(f))
		}
	}
}

func TestTogglerSetReturnsBeforeIO(t *testing.T) {
	block := make(chan struct{})
	s := &swState{readBlock: block}
	tg := newTestToggler(t, s, 50*time.Millisecond)
	defer close(block)
	start := time.Now()
	tg.Set(filterNR, true)
	tg.Set(filterNotch, true)
	if d := time.Since(start); d > 20*time.Millisecond {
		t.Fatalf("Set blocked for %v", d)
	}
}

func TestTogglerConcurrentDuplicatesTapOnce(t *testing.T) {
	s := &swState{}
	tg := newTestToggler(t, s, 50*time.Millisecond)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); tg.Set(filterNR, true) }()
	}
	wg.Wait()
	waitUntil(t, "idle", tg.idle)
	if s.tapCount() != 1 || !s.get(filterNR) {
		t.Fatalf("taps = %d, NR = %v; want 1 tap, NR on", s.tapCount(), s.get(filterNR))
	}
}

func TestTogglerLatestRequestWins(t *testing.T) {
	block := make(chan struct{})
	s := &swState{readBlock: block}
	tg := newTestToggler(t, s, 50*time.Millisecond)
	tg.Set(filterNR, true) // the worker wakes and blocks in read
	time.Sleep(10 * time.Millisecond)
	tg.Set(filterNR, false)
	s.set(func(s *swState) { s.readBlock = nil })
	close(block)
	waitUntil(t, "idle", tg.idle)
	if n := s.tapCount(); n != 0 {
		t.Fatalf("taps = %d, want 0 (on then off before the worker acted)", n)
	}
}

func TestTogglerDelayedEffectIsNotRetapped(t *testing.T) {
	s := &swState{delay: 30 * time.Millisecond}
	tg := newTestToggler(t, s, 200*time.Millisecond)
	tg.Set(filterNR, true)
	waitUntil(t, "idle", tg.idle)
	if s.tapCount() != 1 || !s.get(filterNR) {
		t.Fatalf("taps = %d, NR = %v; want 1 tap, NR on", s.tapCount(), s.get(filterNR))
	}
}

func TestTogglerGivesUpAfterMaxTaps(t *testing.T) {
	s := &swState{ignore: true}
	tg := newTestToggler(t, s, 20*time.Millisecond)
	tg.Set(filterNR, true)
	waitUntil(t, "idle", tg.idle)
	time.Sleep(60 * time.Millisecond)
	if n := s.tapCount(); n != 3 {
		t.Fatalf("taps = %d, want 3", n)
	}
}

func TestTogglerReadErrorSendsNoTap(t *testing.T) {
	s := &swState{readErr: errors.New("DS: query timeout")}
	tg := newTestToggler(t, s, 20*time.Millisecond)
	tg.Set(filterNR, true)
	time.Sleep(30 * time.Millisecond)
	if n := s.tapCount(); n != 0 {
		t.Fatalf("taps = %d while DS fails, want 0", n)
	}
	s.set(func(s *swState) { s.readErr = nil })
	waitUntil(t, "idle", tg.idle)
	if !s.get(filterNR) {
		t.Fatal("NR not on once DS answers again")
	}
}

// NR off; request on (the tap's effect is delayed), then off before it lands.
// The worker must wait for the first tap, then tap again: NR ends off.
func TestTogglerNewerRequestWhileTapPending(t *testing.T) {
	s := &swState{delay: 40 * time.Millisecond}
	tg := newTestToggler(t, s, 500*time.Millisecond)
	tg.Set(filterNR, true)
	waitUntil(t, "first tap", func() bool { return s.tapCount() == 1 })
	tg.Set(filterNR, false)
	waitUntil(t, "idle", tg.idle)
	time.Sleep(60 * time.Millisecond) // anything still in flight lands
	if s.get(filterNR) || s.tapCount() != 2 {
		t.Fatalf("NR = %v, taps = %d; want off after 2 taps", s.get(filterNR), s.tapCount())
	}
}

// A tap sent while the K3 was off is lost; after evidence of a power cycle,
// ForgetPending lets the request be applied at once.
func TestTogglerForgetPendingAfterPowerCycle(t *testing.T) {
	s := &swState{ignore: true}
	tg := newTestToggler(t, s, time.Second)
	tg.Set(filterNR, true)
	waitUntil(t, "first tap", func() bool { return s.tapCount() == 1 })
	s.set(func(s *swState) { s.ignore = false })
	start := time.Now()
	tg.ForgetPending()
	waitUntil(t, "idle", tg.idle)
	if d := time.Since(start); d > 200*time.Millisecond || !s.get(filterNR) {
		t.Fatalf("took %v, NR = %v; want NR on well inside the 1 s settle", d, s.get(filterNR))
	}
}

func TestTogglerToggleFlipsObservedState(t *testing.T) {
	s := &swState{}
	tg := newTestToggler(t, s, 50*time.Millisecond)
	tg.Toggle(filterNR)
	waitUntil(t, "idle", tg.idle)
	if !s.get(filterNR) {
		t.Fatal("NR not on after Toggle")
	}
	tg.Toggle(filterNR)
	waitUntil(t, "idle", tg.idle)
	if s.get(filterNR) || s.tapCount() != 2 {
		t.Fatalf("NR = %v, taps = %d; want off after 2 taps", s.get(filterNR), s.tapCount())
	}
}

func TestTogglerCloseStopsTapping(t *testing.T) {
	s := &swState{ignore: true}
	tg := newToggler(s.read, s.tap)
	tg.settle = 20 * time.Millisecond
	tg.poll = 2 * time.Millisecond
	tg.start()
	tg.Set(filterNR, true)
	waitUntil(t, "first tap", func() bool { return s.tapCount() == 1 })
	tg.Close()
	time.Sleep(60 * time.Millisecond)
	if n := s.tapCount(); n != 1 {
		t.Fatalf("taps = %d after Close, want 1", n)
	}
	tg.Close() // idempotent
}
