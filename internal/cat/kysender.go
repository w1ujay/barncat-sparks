package cat

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// ErrBufferFull is returned when KY text would overflow the sender's queue.
var ErrBufferFull = errors.New("KY text queue full")

var errKYClosed = errors.New("KY sender closed")

// kySender feeds queued KY text to a radio whose text buffer is small and
// reports only "full" or "not full". Before each write it polls room(); "not
// full" proves one free slot, so each write carries exactly one character.
//
// Abort is atomic with respect to writes. Abort first marks itself pending
// (clearing the queue and bumping the generation), then takes sendMu to send
// RX. The sender re-checks under sendMu before each write and writes nothing
// while an abort is pending. So at most the one write already under way
// happens before RX, nothing from the aborted message follows it, and text
// queued during the abort waits until RX is out. Polls run outside sendMu, so
// a slow poll never delays an abort.
type kySender struct {
	room  func() (bool, error) // true when the radio can take one more character
	write func(text string) error
	abort func() error // sends RX

	sendMu sync.Mutex // held across the final generation check and one write, and across Abort

	mu       sync.Mutex // guards queue, gen, aborting, closed; never held across I/O
	queue    []byte     // queue[0] is the next character; it stays counted until written
	gen      uint64     // bumped by Abort; a character selected under an older gen is discarded
	aborting int        // Aborts waiting for sendMu; no write starts while > 0
	closed   bool

	wake    chan struct{}
	done    chan struct{}
	stopped chan struct{}

	pollGap        time.Duration // wait between polls while the buffer is full
	fullTimeout    time.Duration // drop the queue when the buffer stays full this long
	maxQueue       int           // queued characters allowed
	maxFailedPolls int           // consecutive failed polls (timeouts or rejections) before dropping the queue

	afterPoll  func() // test hook: after a poll returns, before any check
	afterCheck func() // test hook: sendMu held and generation checked, before the write
}

func newKYSender(room func() (bool, error), write func(string) error, abort func() error) *kySender {
	return &kySender{
		room:           room,
		write:          write,
		abort:          abort,
		wake:           make(chan struct{}, 1),
		done:           make(chan struct{}),
		stopped:        make(chan struct{}),
		pollGap:        50 * time.Millisecond,
		fullTimeout:    10 * time.Second,
		maxQueue:       512,
		maxFailedPolls: 3,
	}
}

// start launches the sender goroutine. Set tunables and hooks before calling it.
func (s *kySender) start() {
	go s.run()
}

// Enqueue queues text and returns at once. The whole text is admitted or
// none of it is.
func (s *kySender) Enqueue(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errKYClosed
	}
	if len(s.queue)+len(text) > s.maxQueue {
		log.Printf("K3: KY queue full (%d queued), dropping %q", len(s.queue), text)
		return ErrBufferFull
	}
	s.queue = append(s.queue, text...)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// Abort discards all queued text and sends RX. A write already under way
// finishes first; nothing queued before the abort is written after RX.
func (s *kySender) Abort() error {
	s.mu.Lock()
	s.queue = nil
	s.gen++
	s.aborting++
	s.mu.Unlock()

	s.sendMu.Lock()
	err := s.abort()
	s.mu.Lock()
	s.aborting--
	s.mu.Unlock()
	s.sendMu.Unlock()
	return err
}

// exclusive runs fn under sendMu, the lock Abort holds to send RX, so fn's
// write reaches the radio wholly before or wholly after an abort's RX.
func (s *kySender) exclusive(fn func() error) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return fn()
}

// Close stops admissions, aborts (sending RX) and waits for the sender to exit.
func (s *kySender) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.Abort()
	close(s.done)
	<-s.stopped
}

// queued reports how many characters are waiting, including one selected
// but not yet written.
func (s *kySender) queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *kySender) run() {
	defer close(s.stopped)
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.mu.Unlock()
			select {
			case <-s.wake:
			case <-s.done:
				return
			}
			s.mu.Lock()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		ch, gen := s.queue[0], s.gen
		s.mu.Unlock()
		s.sendOne(ch, gen)
	}
}

// stale reports whether gen was aborted, an abort is pending, or the sender
// closed. A character that is stale only because an abort is pending belongs
// to text queued after it; it stays queued and is retried once RX is out.
func (s *kySender) stale(gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen != gen || s.aborting > 0 || s.closed
}

// sendOne polls until the radio has room, then writes ch unless an abort
// intervened. A poll never writes text; only a "not full" KY0 authorizes the
// write below, so retrying a failed or rejected poll cannot duplicate an
// on-air character.
func (s *kySender) sendOne(ch byte, gen uint64) {
	var fullSince time.Time
	failedPolls := 0
	for {
		room, err := s.room()
		if s.afterPoll != nil {
			s.afterPoll()
		}
		if s.stale(gen) {
			return
		}
		if err != nil {
			// A timeout and a rejection (an unrelated passthrough set's late
			// "?;", per rawQuery's rawRejectGrace) both count toward the same
			// consecutive-failure cap: either can be a stray, and retrying
			// costs only a poll, not a written character.
			if errors.Is(err, ErrQueryTimeout) || errors.Is(err, ErrRadioRejected) {
				failedPolls++
				if failedPolls < s.maxFailedPolls {
					continue
				}
			}
			s.drop(gen, err)
			return
		}
		failedPolls = 0
		if !room {
			if fullSince.IsZero() {
				fullSince = time.Now()
			} else if time.Since(fullSince) >= s.fullTimeout {
				s.drop(gen, fmt.Errorf("radio text buffer full for %v", s.fullTimeout))
				return
			}
			select {
			case <-time.After(s.pollGap):
			case <-s.done:
				return
			}
			continue
		}

		s.sendMu.Lock()
		if s.stale(gen) {
			s.sendMu.Unlock()
			return
		}
		if s.afterCheck != nil {
			s.afterCheck()
		}
		err = s.write(string([]byte{ch}))
		s.mu.Lock()
		if s.gen == gen && len(s.queue) > 0 {
			s.queue = s.queue[1:]
		}
		s.mu.Unlock()
		s.sendMu.Unlock()
		if err != nil {
			// The write may have reached the radio; retrying could send the
			// character twice, so drop the rest of this message instead.
			s.drop(gen, err)
		}
		return
	}
}

// drop discards the queue if it still belongs to gen.
func (s *kySender) drop(gen uint64, why error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return
	}
	if len(s.queue) > 0 {
		log.Printf("K3: dropping %d queued KY characters: %v", len(s.queue), why)
	}
	s.queue = nil
}
