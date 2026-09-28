package cat

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// swState simulates the K3's NR/NTCH switches for toggler tests.
type swState struct {
	mu          sync.Mutex
	on          [2]bool
	taps        []toggleFilter
	readErr     error
	ignore      bool          // taps have no effect (K3 off)
	delay       time.Duration // time from a tap to its effect
	readBlock   chan struct{} // when set, read waits on it
	tapBlock    chan struct{} // when set, tap waits on it before recording
	readEntries int           // read() calls entered so far, including ones still blocked
	tapEntries  int           // tap() calls entered so far, including ones still blocked
}

// read snapshots the switch state (and any error) before blocking, not
// after: it models a query whose reply is already determined but delayed in
// arriving, so a test can change swState after the snapshot and still see
// the toggler act on the value the "reply" actually carries.
func (s *swState) read() (bool, bool, error) {
	s.mu.Lock()
	s.readEntries++
	nr, notch, err := s.on[filterNR], s.on[filterNotch], s.readErr
	block := s.readBlock
	s.mu.Unlock()
	if block != nil {
		<-block
	}
	if err != nil {
		return false, false, err
	}
	return nr, notch, nil
}

func (s *swState) tap(f toggleFilter) error {
	s.mu.Lock()
	s.tapEntries++
	block := s.tapBlock
	s.mu.Unlock()
	if block != nil {
		<-block
	}
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

func (s *swState) readEntryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readEntries
}

func (s *swState) tapEntryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tapEntries
}

func (s *swState) tapCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.taps)
}

func (s *swState) filterTapCount(f toggleFilter) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, tf := range s.taps {
		if tf == f {
			n++
		}
	}
	return n
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

// A duplicate Set for the value already outstanding must not reset the
// retry counter: otherwise a client that keeps re-sending "NR on" while the
// K3 ignores taps (never converges) prevents the cap from ever being
// reached, retrying forever instead of giving up after maxTaps.
func TestTogglerDuplicateSetDoesNotResetRetryCap(t *testing.T) {
	s := &swState{ignore: true}
	settle := 20 * time.Millisecond
	tg := newTestToggler(t, s, settle)
	tg.Set(filterNR, true)
	// Resend the same request well before the worker could give up (which
	// takes about 3*settle): each resend must coalesce, not restart.
	for range 5 {
		time.Sleep(settle / 2)
		tg.Set(filterNR, true)
	}
	waitUntil(t, "idle", tg.idle)
	if n := s.tapCount(); n != 3 {
		t.Fatalf("taps = %d, want 3 (duplicate Set must not reset the retry cap)", n)
	}
}

func TestTogglerDuplicateSetWhilePendingSendsNoExtraTap(t *testing.T) {
	s := &swState{delay: 30 * time.Millisecond}
	tg := newTestToggler(t, s, 200*time.Millisecond)
	tg.Set(filterNR, true)
	waitUntil(t, "first tap", func() bool { return s.tapCount() == 1 })
	tg.Set(filterNR, true) // duplicate while the tap is still pending
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

// ForgetPending racing a tap() call that hasn't returned yet must not drop
// that tap's tracking: otherwise the tap can still land while the worker,
// believing nothing is pending, sends a second one for the same request.
func TestTogglerForgetPendingDuringTapNotRetapped(t *testing.T) {
	block := make(chan struct{})
	s := &swState{delay: 10 * time.Millisecond, tapBlock: block}
	tg := newTestToggler(t, s, 500*time.Millisecond)
	tg.Set(filterNR, true)
	waitUntil(t, "tap entered", func() bool { return s.tapEntryCount() == 1 })

	tg.ForgetPending() // races the still-blocked tap() call

	close(block)
	waitUntil(t, "idle", tg.idle)
	if n := s.tapCount(); n != 1 {
		t.Fatalf("taps = %d, want 1 (ForgetPending mid-tap must not cause a retap)", n)
	}
	if !s.get(filterNR) {
		t.Fatal("NR not on after the delayed tap landed")
	}
}

// ForgetPending racing a read() call that hasn't returned yet must discard
// that reading: it was taken before the reset ForgetPending responds to, so
// acting on it could be wrong. Here NR is actually already on by the time
// the stale "off" reading is delivered, so acting on it would wrongly tap
// NR back off; the worker must instead re-read and see it's already met.
func TestTogglerForgetPendingDuringReadDiscardsStaleReading(t *testing.T) {
	block := make(chan struct{})
	s := &swState{readBlock: block} // NR off when the query is captured
	tg := newTestToggler(t, s, 50*time.Millisecond)
	tg.Set(filterNR, true)
	waitUntil(t, "read entered", func() bool { return s.readEntryCount() == 1 })

	// The radio's actual state moves to "on" as part of the same reset
	// ForgetPending responds to; the in-flight read already captured "off".
	s.set(func(s *swState) { s.on[filterNR] = true })
	tg.ForgetPending()

	s.set(func(s *swState) { s.readBlock = nil })
	close(block) // deliver the stale "off" reading
	waitUntil(t, "idle", tg.idle)
	if n := s.tapCount(); n != 0 {
		t.Fatalf("taps = %d, want 0 (NR was already on; the stale reading must not cause a tap)", n)
	}
	if !s.get(filterNR) {
		t.Fatal("NR should already be on")
	}
}

// ForgetPending can land while an earlier filter's tap() call is still in
// flight, i.e. between that filter's lock and the next filter's lock in the
// same pass. The next filter's half of that pass's reading is just as stale
// as one taken before read() itself, and must be discarded the same way.
func TestTogglerForgetPendingDuringOtherFilterTapDiscardsReading(t *testing.T) {
	tapBlock := make(chan struct{})
	s := &swState{tapBlock: tapBlock} // NR and notch both off
	tg := newTestToggler(t, s, 500*time.Millisecond)
	tg.Set(filterNR, true)    // filter 0: dispatched first, tap() blocks
	tg.Set(filterNotch, true) // filter 1: judged against this pass's reading
	waitUntil(t, "NR tap entered", func() bool { return s.tapEntryCount() == 1 })

	// Notch's actual state moves to "on" (e.g. the same reset) while NR's
	// tap() call — and so filter 1's turn in this pass — is still blocked.
	// This pass's `observed` for notch was already captured as "off".
	s.set(func(s *swState) { s.on[filterNotch] = true })
	tg.ForgetPending()

	close(tapBlock) // let NR's tap() return; the pass can reach filter 1
	waitUntil(t, "idle", tg.idle)
	if n := s.filterTapCount(filterNotch); n != 0 {
		t.Fatalf("notch tapped %d times, want 0 (must not act on the pre-reset reading)", n)
	}
	if !s.get(filterNotch) {
		t.Fatal("notch should already be on")
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
	// Close only promises no tap after it returns, not that exactly one tap
	// happened before it: with a short settle, a second tap can legitimately
	// land before Close is called if this goroutine is scheduled late.
	n := s.tapCount()
	time.Sleep(60 * time.Millisecond)
	if m := s.tapCount(); m != n {
		t.Fatalf("taps = %d after Close, want unchanged from %d", m, n)
	}
	tg.Close() // idempotent
}

// Close must wait out a tap() call already in flight rather than abandoning
// it, and must send no further tap once it returns.
func TestTogglerCloseWhileTapBlocked(t *testing.T) {
	block := make(chan struct{})
	s := &swState{tapBlock: block}
	tg := newToggler(s.read, s.tap)
	tg.settle = 500 * time.Millisecond
	tg.poll = 2 * time.Millisecond
	tg.start()
	tg.Set(filterNR, true)
	waitUntil(t, "tap entered", func() bool { return s.tapEntryCount() == 1 })

	closed := make(chan struct{})
	go func() { tg.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while tap() was still blocked")
	case <-time.After(20 * time.Millisecond):
	}

	close(block)
	<-closed
	if n := s.tapCount(); n != 1 {
		t.Fatalf("taps = %d after Close, want 1", n)
	}
}
