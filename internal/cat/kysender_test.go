package cat

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// kyBuf simulates the K3's KY text buffer for kySender tests.
type kyBuf struct {
	mu       sync.Mutex
	capacity int
	buffered int
	got      []string // every write in order; "RX" marks an abort
	overflow int
	polls    int
	roomErr  error
	writeErr error
}

func (b *kyBuf) room() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.polls++
	if b.roomErr != nil {
		return false, b.roomErr
	}
	return b.buffered < b.capacity, nil
}

func (b *kyBuf) write(text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, text)
	if b.writeErr != nil {
		return b.writeErr
	}
	for range text {
		if b.buffered >= b.capacity {
			b.overflow++
		} else {
			b.buffered++
		}
	}
	return nil
}

func (b *kyBuf) abort() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, "RX")
	return nil
}

func (b *kyBuf) drain(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffered = max(0, b.buffered-n)
}

func (b *kyBuf) writes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.got...)
}

func (b *kyBuf) set(fn func(b *kyBuf)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn(b)
}

// barrier pauses the first goroutine that calls hook until release.
type barrier struct {
	reached, release chan struct{}
	once             sync.Once
}

func newBarrier() *barrier {
	return &barrier{reached: make(chan struct{}), release: make(chan struct{})}
}

func (b *barrier) hook() {
	b.once.Do(func() {
		close(b.reached)
		<-b.release
	})
}

func (b *barrier) wait(t *testing.T) {
	t.Helper()
	select {
	case <-b.reached:
	case <-time.After(2 * time.Second):
		t.Fatal("barrier not reached")
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// newTestSender builds a sender over b with short timings; setup runs before
// start, to set hooks.
func newTestSender(t *testing.T, b *kyBuf, setup func(s *kySender)) *kySender {
	t.Helper()
	s := newKYSender(b.room, b.write, b.abort)
	s.pollGap = time.Millisecond
	s.fullTimeout = time.Second
	if setup != nil {
		setup(s)
	}
	s.start()
	t.Cleanup(s.Close)
	return s
}

func TestKYSenderOneCharPerWriteNoOverflow(t *testing.T) {
	b := &kyBuf{capacity: 32}
	stop := make(chan struct{})
	defer close(stop)
	go func() { // the K3 sending one character per millisecond
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				b.drain(1)
			}
		}
	}()
	s := newTestSender(t, b, nil)
	text := strings.Repeat("CQ TEST DE NZ1U ", 13)[:200]
	if err := s.Enqueue(text); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "200 writes", func() bool { return len(b.writes()) == 200 })
	for i, w := range b.writes() {
		if len(w) != 1 {
			t.Fatalf("write %d = %q; want exactly one character", i, w)
		}
	}
	if got := strings.Join(b.writes(), ""); got != text {
		t.Fatalf("sent %q, want %q", got, text)
	}
	if b.overflow != 0 {
		t.Fatalf("overflow = %d, want 0", b.overflow)
	}
}

func TestKYSenderStopsWhenFull(t *testing.T) {
	for _, start := range []int{24, 31} {
		t.Run(fmt.Sprintf("occupancy %d", start), func(t *testing.T) {
			b := &kyBuf{capacity: 32, buffered: start}
			s := newTestSender(t, b, nil)
			if err := s.Enqueue(strings.Repeat("E", 20)); err != nil {
				t.Fatal(err)
			}
			free := 32 - start
			waitUntil(t, "buffer full", func() bool { return len(b.writes()) == free })
			time.Sleep(20 * time.Millisecond)
			if n := len(b.writes()); n != free {
				t.Fatalf("writes = %d after the buffer filled, want %d", n, free)
			}
			b.drain(1)
			waitUntil(t, "one more write", func() bool { return len(b.writes()) == free+1 })
			time.Sleep(20 * time.Millisecond)
			if n := len(b.writes()); n != free+1 {
				t.Fatalf("writes = %d after draining one slot, want %d", n, free+1)
			}
			if b.overflow != 0 {
				t.Fatalf("overflow = %d, want 0", b.overflow)
			}
		})
	}
}

func TestKYSenderEnqueueDoesNotBlock(t *testing.T) {
	b := &kyBuf{capacity: 32}
	gate := newBarrier()
	s := newTestSender(t, b, func(s *kySender) { s.afterPoll = gate.hook })
	defer close(gate.release)
	s.Enqueue("A")
	gate.wait(t) // the sender is now stuck mid-poll
	start := time.Now()
	if err := s.Enqueue("BCD"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Enqueue blocked for %v", d)
	}
}

// Abort lands after the poll but before the sender takes sendMu.
func TestKYSenderAbortBetweenPollAndSend(t *testing.T) {
	for _, how := range []string{"Abort", "Close"} {
		t.Run(how, func(t *testing.T) {
			b := &kyBuf{capacity: 32}
			gate := newBarrier()
			s := newTestSender(t, b, func(s *kySender) { s.afterPoll = gate.hook })
			s.Enqueue("ABC")
			gate.wait(t)
			var closeDone chan struct{}
			if how == "Abort" {
				s.Abort()
			} else {
				closeDone = make(chan struct{})
				go func() { s.Close(); close(closeDone) }() // Close waits for the sender, which is paused
				waitUntil(t, "RX", func() bool { return len(b.writes()) == 1 })
			}
			close(gate.release)
			time.Sleep(20 * time.Millisecond)
			if got := b.writes(); len(got) != 1 || got[0] != "RX" {
				t.Fatalf("writes = %v, want only [RX]", got)
			}
			if how == "Abort" {
				s.Enqueue("Z")
				waitUntil(t, "new text", func() bool { return len(b.writes()) == 2 })
				if got := b.writes(); got[1] != "Z" {
					t.Fatalf("writes = %v, want [RX Z]", got)
				}
			}
			if closeDone != nil {
				select {
				case <-closeDone:
				case <-time.After(2 * time.Second):
					t.Fatal("Close goroutine did not finish")
				}
			}
		})
	}
}

// TestKYSenderAbortDuringStalledPoll: a KY poll that hangs (the radio is
// slow) must not delay RX.
func TestKYSenderAbortDuringStalledPoll(t *testing.T) {
	b := &kyBuf{capacity: 32}
	stall := make(chan struct{})
	entered := make(chan struct{})
	var enterOnce sync.Once
	room := func() (bool, error) {
		enterOnce.Do(func() { close(entered) })
		<-stall
		return true, nil
	}
	s := newKYSender(room, b.write, b.abort)
	s.start()
	t.Cleanup(func() { close(stall); s.Close() })
	s.Enqueue("A")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("poll never started")
	}
	done := make(chan struct{})
	go func() { s.Abort(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Abort waited for a stalled poll")
	}
}

// The sender has checked the generation and holds sendMu: abort waits for
// that one write, then sends RX, and the rest is discarded.
func TestKYSenderAbortWaitsForInFlightWrite(t *testing.T) {
	b := &kyBuf{capacity: 32}
	gate := newBarrier()
	s := newTestSender(t, b, func(s *kySender) { s.afterCheck = gate.hook })
	s.Enqueue("AB")
	gate.wait(t)
	aborted := make(chan struct{})
	go func() { s.Abort(); close(aborted) }()
	time.Sleep(20 * time.Millisecond)
	if len(b.writes()) != 0 {
		t.Fatalf("writes = %v before the in-flight write finished", b.writes())
	}
	close(gate.release)
	<-aborted
	time.Sleep(20 * time.Millisecond)
	if got := b.writes(); len(got) != 2 || got[0] != "A" || got[1] != "RX" {
		t.Fatalf("writes = %v, want [A RX]", got)
	}
}

// Text queued while an abort waits for the in-flight write must not be
// written before RX.
func TestKYSenderTextDuringAbortWaitsForRX(t *testing.T) {
	b := &kyBuf{capacity: 32}
	gate := newBarrier()
	s := newTestSender(t, b, func(s *kySender) { s.afterCheck = gate.hook })
	s.Enqueue("A")
	gate.wait(t)
	aborted := make(chan struct{})
	go func() { s.Abort(); close(aborted) }()
	waitUntil(t, "abort pending", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.aborting > 0
	})
	s.Enqueue("Z")
	close(gate.release)
	<-aborted
	waitUntil(t, "Z sent", func() bool { return len(b.writes()) == 3 })
	if got := strings.Join(b.writes(), ","); got != "A,RX,Z" {
		t.Fatalf("writes = %s, want A,RX,Z", got)
	}
}

// An old poll that errors out after an abort must not drop text admitted
// after the abort. The poll returns ErrRadioRejected (not ErrQueryTimeout),
// and maxFailedPolls is set to 1 here so a single rejection is already at
// the cap: absent the post-poll staleness check, sendOne's error handling
// would call drop immediately, with no retry (gen has already advanced by
// the time the barrier below releases, so this exercises that staleness
// check; see TestKYSenderDropGuardProtectsTextAfterConcurrentAbort for a
// case that instead exercises drop's own generation guard).
func TestKYSenderStalePollDoesNotDropNewText(t *testing.T) {
	b := &kyBuf{capacity: 32}
	gate := newBarrier()
	var calls int
	room := func() (bool, error) {
		calls++
		if calls == 1 {
			return false, fmt.Errorf("KY: %w", ErrRadioRejected)
		}
		return b.room()
	}
	s := newKYSender(room, b.write, b.abort)
	s.pollGap = time.Millisecond
	s.maxFailedPolls = 1
	s.afterPoll = gate.hook
	s.start()
	t.Cleanup(s.Close)
	s.Enqueue("OLD")
	gate.wait(t)
	s.Abort()
	s.Enqueue("NEW")
	close(gate.release)
	waitUntil(t, "NEW sent", func() bool { return len(b.writes()) == 4 })
	if got := strings.Join(b.writes(), ","); got != "RX,N,E,W" {
		t.Fatalf("writes = %s, want RX,N,E,W", got)
	}
}

// drop's own generation guard (kysender.go's drop, independent of the
// post-poll staleness check in sendOne) must protect text admitted after a
// concurrent abort. sendMu is held for the whole write, so an Abort
// triggered from inside a failing write can bump the generation (which
// only needs the state mutex) but cannot send RX until the write returns
// and releases sendMu. This forces that exact ordering and admits NEW
// before the failing write returns, so by the time sendOne calls drop for
// the stale write, the post-poll staleness check has already passed (long
// before this write started) and only drop's own generation guard stands
// between the failed write and wiping NEW.
func TestKYSenderDropGuardProtectsTextAfterConcurrentAbort(t *testing.T) {
	b := &kyBuf{capacity: 32}
	var s *kySender
	var once sync.Once
	writeFailure := errors.New("simulated write failure")
	write := func(text string) error {
		fire := false
		once.Do(func() { fire = true })
		if !fire {
			return b.write(text)
		}
		s.mu.Lock()
		g0 := s.gen
		s.mu.Unlock()
		go s.Abort() // blocks on sendMu, held by this write, after bumping gen
		// Poll for the generation bump. This runs on the sender's own
		// goroutine (inside write), not the test goroutine, so it must not
		// call t.Fatal/Fatalf (unsafe off the test goroutine); t.Error is
		// safe from any goroutine and the outer waitUntil below still times
		// out and fails the test if the bump never happens.
		deadline := time.Now().Add(2 * time.Second)
		for {
			s.mu.Lock()
			bumped := s.gen != g0
			s.mu.Unlock()
			if bumped {
				break
			}
			if time.Now().After(deadline) {
				t.Error("generation never bumped")
				break
			}
			time.Sleep(time.Millisecond)
		}
		if err := s.Enqueue("NEW"); err != nil {
			t.Errorf("Enqueue(NEW) during abort: %v", err)
		}
		return writeFailure
	}
	s = newKYSender(b.room, write, b.abort)
	s.pollGap = time.Millisecond
	s.start()
	t.Cleanup(s.Close)
	if err := s.Enqueue("OLD"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "NEW sent", func() bool { return len(b.writes()) == 4 })
	if got := strings.Join(b.writes(), ","); got != "RX,N,E,W" {
		t.Fatalf("writes = %s, want RX,N,E,W", got)
	}
}

func TestKYSenderDropsQueueWhenFullTooLong(t *testing.T) {
	b := &kyBuf{capacity: 1, buffered: 1}
	s := newTestSender(t, b, func(s *kySender) { s.fullTimeout = 30 * time.Millisecond })
	s.Enqueue("ABC")
	waitUntil(t, "queue dropped", func() bool { return s.queued() == 0 })
	if len(b.writes()) != 0 {
		t.Fatalf("writes = %v, want none", b.writes())
	}
	b.drain(1)
	s.Enqueue("D")
	waitUntil(t, "D sent", func() bool { return len(b.writes()) == 1 })
}

func TestKYSenderDropsQueueAfterThreeTimeouts(t *testing.T) {
	b := &kyBuf{capacity: 32, roomErr: fmt.Errorf("KY: %w", ErrQueryTimeout)}
	s := newTestSender(t, b, nil)
	s.Enqueue("AB")
	waitUntil(t, "queue dropped", func() bool { return s.queued() == 0 })
	b.mu.Lock()
	polls := b.polls
	b.mu.Unlock()
	if polls != 3 || len(b.writes()) != 0 {
		t.Fatalf("polls = %d, writes = %v; want 3 polls and no writes", polls, b.writes())
	}
}

// A persistently rejecting radio (every poll answers "?;") counts each
// rejection toward the same consecutive-failure cap as a timeout, and drops
// the queue only once the cap is reached, without ever writing.
func TestKYSenderPersistentRejectionDropsAfterThreePolls(t *testing.T) {
	b := &kyBuf{capacity: 32, roomErr: ErrRadioRejected}
	s := newTestSender(t, b, nil)
	s.Enqueue("AB")
	waitUntil(t, "queue dropped", func() bool { return s.queued() == 0 })
	b.mu.Lock()
	polls := b.polls
	writes := len(b.got)
	b.mu.Unlock()
	if polls != 3 || writes != 0 {
		t.Fatalf("polls = %d, writes = %d; want 3 polls and no writes", polls, writes)
	}
}

// A stray rejection (one "?;" from an unrelated passthrough set, per
// rawQuery's rawRejectGrace) must not drop queued CW: it counts as one
// failed poll, well under the cap, and the sender simply re-polls.
func TestKYSenderStrayRejectionDoesNotDropCW(t *testing.T) {
	b := &kyBuf{capacity: 32}
	var calls int
	room := func() (bool, error) {
		calls++
		if calls == 1 {
			return false, ErrRadioRejected
		}
		return b.room()
	}
	s := newKYSender(room, b.write, b.abort)
	s.pollGap = time.Millisecond
	s.start()
	t.Cleanup(s.Close)
	if err := s.Enqueue("ABC"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "ABC sent", func() bool { return len(b.writes()) == 3 })
	if got := strings.Join(b.writes(), ""); got != "ABC" {
		t.Fatalf("sent %q, want %q", got, "ABC")
	}
}

func TestKYSenderWriteErrorDropsWithoutRetry(t *testing.T) {
	b := &kyBuf{capacity: 32, writeErr: errors.New("serial write failed")}
	s := newTestSender(t, b, nil)
	s.Enqueue("AB")
	waitUntil(t, "queue dropped", func() bool { return s.queued() == 0 })
	time.Sleep(20 * time.Millisecond)
	if got := b.writes(); len(got) != 1 || got[0] != "A" {
		t.Fatalf("writes = %v, want [A] with no retry", got)
	}
}

func TestKYSenderQueueLimitIsAllOrNothing(t *testing.T) {
	b := &kyBuf{capacity: 0} // never room: everything stays queued
	gate := newBarrier()
	s := newTestSender(t, b, func(s *kySender) { s.afterPoll = gate.hook })
	defer close(gate.release)
	if err := s.Enqueue(strings.Repeat("E", 500)); err != nil {
		t.Fatal(err)
	}
	gate.wait(t) // one character is selected but not written: still counted
	if err := s.Enqueue(strings.Repeat("E", 13)); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("err = %v, want ErrBufferFull", err)
	}
	if n := s.queued(); n != 500 {
		t.Fatalf("queued = %d, want 500 (no partial admission)", n)
	}
	if err := s.Enqueue(strings.Repeat("E", 12)); err != nil {
		t.Fatalf("512 total should fit: %v", err)
	}
}

func TestKYSenderCloseSendsRX(t *testing.T) {
	b := &kyBuf{capacity: 32}
	s := newKYSender(b.room, b.write, b.abort)
	s.start()
	s.Close()
	if got := b.writes(); len(got) != 1 || got[0] != "RX" {
		t.Fatalf("writes = %v, want [RX]", got)
	}
	if err := s.Enqueue("A"); err == nil {
		t.Fatal("Enqueue after Close succeeded")
	}
	s.Close() // idempotent
}

// A byte of 0x80 or above (RTTY text is bytes, not runes) is written as that
// one byte; string(ch) would UTF-8-encode 0xC3 into two.
func TestKYSenderWritesHighByteAsOneByte(t *testing.T) {
	b := &kyBuf{capacity: 32}
	s := newTestSender(t, b, nil)
	if err := s.Enqueue("\xc3"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "write", func() bool { return len(b.writes()) == 1 })
	if got := b.writes()[0]; got != "\xc3" {
		t.Fatalf("wrote %q (%d bytes), want the single byte 0xC3", got, len(got))
	}
}
