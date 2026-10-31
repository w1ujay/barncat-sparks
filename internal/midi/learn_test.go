package midi

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLearnedFrom(t *testing.T) {
	tests := []struct {
		name           string
		status, d1, d2 byte
		want           Learned
		ok             bool
	}{
		{"cc", 0xB2, 22, 1, Learned{Type: "cc", Channel: 2, Number: 22}, true},
		{"cc at zero", 0xB0, 8, 0, Learned{Type: "cc", Channel: 0, Number: 8}, true},
		{"note on", 0x91, 7, 127, Learned{Type: "note_on", Channel: 1, Number: 7}, true},
		{"note on velocity 0 is a release", 0x91, 7, 0, Learned{}, false},
		{"note off", 0x81, 7, 0, Learned{}, false},
		{"pitch bend", 0xE0, 0, 64, Learned{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := learnedFrom(tt.status, tt.d1, tt.d2)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("got %+v %v, want %+v %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// startLearn runs Learn and returns once it is waiting.
func startLearn(t *testing.T, d *MIDIDriver, ctx context.Context) <-chan learnResult {
	t.Helper()
	out := make(chan learnResult, 1)
	go func() {
		got, err := d.Learn(ctx)
		out <- learnResult{got, err}
	}()
	for range 200 {
		d.learn.mu.Lock()
		waiting := d.learn.ch != nil
		d.learn.mu.Unlock()
		if waiting {
			return out
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Learn never started waiting")
	return nil
}

type learnResult struct {
	got Learned
	err error
}

// TestLearnTakesPressWithoutRunningAction: pressing a mapped control to learn
// it must not run its action, or learning a PTT button would key the radio.
func TestLearnTakesPressWithoutRunningAction(t *testing.T) {
	r := &pttRadio{}
	d := newTestDriver(r)
	out := startLearn(t, d, context.Background())

	d.handleMIDIEvent(0x90, pttNote, 127)
	res := <-out
	if res.err != nil || res.got != (Learned{Type: "note_on", Number: pttNote}) {
		t.Fatalf("Learn = %+v, want note_on %d", res, pttNote)
	}
	if got, _ := r.snapshot(); len(got) != 0 {
		t.Fatalf("PTT calls = %v, want none", got)
	}

	// Learn is one-shot: the next press acts as usual.
	d.handleMIDIEvent(0x90, pttNote, 127)
	d.handleMIDIEvent(0x80, pttNote, 0)
	if got, _ := r.snapshot(); len(got) != 2 {
		t.Fatalf("PTT calls after Learn = %v, want press and release", got)
	}
}

// TestLearnStillReleasesHeldPTT: a Learn must never swallow a release, or the
// radio stays keyed.
func TestLearnStillReleasesHeldPTT(t *testing.T) {
	r := &pttRadio{}
	d := newTestDriver(r)
	d.handleMIDIEvent(0x90, pttNote, 127)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := startLearn(t, d, ctx)
	d.handleMIDIEvent(0x80, pttNote, 0)
	d.handleMIDIEvent(0x90, pttNote+1, 0)

	if got, _ := r.snapshot(); len(got) != 2 || got[1] {
		t.Fatalf("PTT calls = %v, want press then release", got)
	}
	cancel()
	if res := <-out; !errors.Is(res.err, context.Canceled) {
		t.Fatalf("Learn after cancel = %+v, want context.Canceled", res)
	}
}

func TestLearnTimeoutLeavesDriverNormal(t *testing.T) {
	r := &pttRadio{}
	d := newTestDriver(r)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := d.Learn(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Learn = %v, want deadline exceeded", err)
	}
	d.handleMIDIEvent(0x90, pttNote, 127)
	d.handleMIDIEvent(0x80, pttNote, 0)
	if got, _ := r.snapshot(); len(got) != 2 {
		t.Fatalf("PTT calls = %v, want press and release after a timed-out Learn", got)
	}
}

func TestLearnBusy(t *testing.T) {
	d := newTestDriver(&pttRadio{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := startLearn(t, d, ctx)
	if _, err := d.Learn(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Learn = %v, want ErrBusy", err)
	}
	cancel()
	<-out
}

func TestLearnEndsWhenDriverCloses(t *testing.T) {
	d := newTestDriver(&pttRadio{})
	out := startLearn(t, d, context.Background())
	d.Close()
	if res := <-out; !errors.Is(res.err, ErrNotConnected) {
		t.Fatalf("Learn after Close = %+v, want ErrNotConnected", res)
	}
}
