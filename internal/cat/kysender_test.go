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
			s.Enqueue(strings.Repeat("E", 20))
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
	if d := time.Since(start); d > 50*time.Millisecond {
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
			if how == "Abort" {
				s.Abort()
			} else {
				go s.Close() // Close waits for the sender, which is paused
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
		})
	}
}

// TestKYSenderAbortDuringStalledPoll: a KY poll that hangs (the radio is
// slow) must not delay RX.
func TestKYSenderAbortDuringStalledPoll(t *testing.T) {
	b := &kyBuf{capacity: 32}
	stall := make(chan struct{})
	s := newKYSender(func() (bool, error) { <-stall; return true, nil }, b.write, b.abort)
	s.start()
	t.Cleanup(func() { close(stall); s.Close() })
	s.Enqueue("A")
	time.Sleep(10 * time.Millisecond)
	done := make(chan struct{})
	go func() { s.Abort(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
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

// An old poll that times out after an abort must not drop text admitted
// after the abort.
func TestKYSenderStalePollDoesNotDropNewText(t *testing.T) {
	b := &kyBuf{capacity: 32}
	gate := newBarrier()
	var calls int
	room := func() (bool, error) {
		calls++
		if calls == 1 {
			return false, fmt.Errorf("KY: %w", ErrQueryTimeout)
		}
		return b.room()
	}
	s := newKYSender(room, b.write, b.abort)
	s.pollGap = time.Millisecond
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

func TestKYSenderRejectedPollDropsWithoutRetry(t *testing.T) {
	b := &kyBuf{capacity: 32, roomErr: ErrRadioRejected}
	s := newTestSender(t, b, nil)
	s.Enqueue("AB")
	waitUntil(t, "queue dropped", func() bool { return s.queued() == 0 })
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.polls != 1 {
		t.Fatalf("polls = %d, want 1", b.polls)
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
