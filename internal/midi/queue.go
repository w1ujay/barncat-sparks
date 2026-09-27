package midi

import "sync"

// eventQueue carries messages from the winmm callback, which must never
// block, to the event loop. Droppable messages (Control Change: jogs and
// knobs) are dropped once softCap are pending, so a stalled executor loses
// tuning steps but never the note-off that releases PTT or the MIM_CLOSE
// that reports a disconnect. hardCap bounds memory: past it every message is
// dropped and drain reports the loss, so the consumer can release PTT.
type eventQueue struct {
	mu    sync.Mutex
	buf   []midiEvent
	soft  int
	hard  int
	lost  bool
	ready chan struct{} // one pending wakeup for the consumer
}

func newEventQueue(softCap, hardCap int) *eventQueue {
	return &eventQueue{soft: softCap, hard: hardCap, ready: make(chan struct{}, 1)}
}

// push queues ev without blocking.
func (q *eventQueue) push(ev midiEvent, droppable bool) {
	q.mu.Lock()
	switch {
	case len(q.buf) >= q.hard:
		q.lost = true
	case droppable && len(q.buf) >= q.soft:
	default:
		q.buf = append(q.buf, ev)
	}
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// drain takes every queued message, and whether any non-droppable message
// was lost since the last drain.
func (q *eventQueue) drain() ([]midiEvent, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	evs, lost := q.buf, q.lost
	q.buf, q.lost = nil, false
	return evs, lost
}
