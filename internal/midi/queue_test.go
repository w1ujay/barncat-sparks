package midi

import "testing"

// TestQueueDropsOnlyControlChangeWhenFull: a flood of jog CCs must not push
// out a PTT note-off or a disconnect queued behind it.
func TestQueueDropsOnlyControlChangeWhenFull(t *testing.T) {
	q := newEventQueue(4, 100)
	for i := range 10 {
		q.push(midiEvent{status: 0xB0, data1: byte(i)}, true) // jog CC
	}
	q.push(midiEvent{status: 0x80, data1: 60}, false) // note off
	q.push(midiEvent{wMsg: 0x3C2}, false)             // MIM_CLOSE

	evs, lost := q.drain()
	if lost {
		t.Error("lost = true, want false: only droppable CCs were dropped")
	}
	if len(evs) != 6 {
		t.Fatalf("drained %d events, want 4 CCs + note off + close", len(evs))
	}
	if evs[4].status != 0x80 || evs[5].wMsg != 0x3C2 {
		t.Errorf("tail = %+v, want the note off then the close", evs[4:])
	}
	if evs, _ := q.drain(); len(evs) != 0 {
		t.Errorf("second drain = %v, want empty", evs)
	}
}

// TestQueueReportsLossPastHardCap: past the hard cap even keep-events are
// dropped, and the consumer is told so it can release PTT.
func TestQueueReportsLossPastHardCap(t *testing.T) {
	q := newEventQueue(2, 3)
	for range 5 {
		q.push(midiEvent{status: 0x90, data1: 60, data2: 127}, false)
	}
	evs, lost := q.drain()
	if len(evs) != 3 || !lost {
		t.Errorf("drained %d, lost=%v; want 3 and true", len(evs), lost)
	}
	if _, lost := q.drain(); lost {
		t.Error("lost not reset by drain")
	}
}

// TestQueueSignalsReady: a push wakes the consumer, and many pushes leave
// one wakeup, not a blocked callback.
func TestQueueSignalsReady(t *testing.T) {
	q := newEventQueue(4, 8)
	q.push(midiEvent{status: 0x90}, false)
	q.push(midiEvent{status: 0x90}, false)
	select {
	case <-q.ready:
	default:
		t.Fatal("no ready signal after push")
	}
	select {
	case <-q.ready:
		t.Fatal("second ready signal; want one pending wakeup")
	default:
	}
}
