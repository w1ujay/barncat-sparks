package hidknob

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ujay/barncat-sparks/internal/controls"
)

// fakeTransport hands out queued packets, returns errTimeout when none is
// waiting, and returns an injected error once to simulate an unplug.
type fakeTransport struct {
	reports chan []byte
	fail    chan error
	closed  chan struct{}
	once    sync.Once
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{reports: make(chan []byte, 16), fail: make(chan error, 1), closed: make(chan struct{})}
}

func (f *fakeTransport) Read(buf []byte) (int, error) {
	select {
	case p := <-f.reports:
		return copy(buf, p), nil
	case err := <-f.fail:
		return 0, err
	case <-f.closed:
		return 0, errors.New("fake transport closed")
	case <-time.After(5 * time.Millisecond):
		return 0, errTimeout
	}
}

func (f *fakeTransport) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeTransport) isClosed() bool {
	select {
	case <-f.closed:
		return true
	default:
		return false
	}
}

type openResult struct {
	t   transport
	st  State
	err error
}

// stepOpener returns whatever the test sends next on steps, so each open
// attempt happens exactly when the test wants it.
func stepOpener(steps chan openResult) opener {
	return func() (transport, State, error) {
		r := <-steps
		return r.t, r.st, r.err
	}
}

func alwaysOpen(t transport) opener {
	return func() (transport, State, error) { return t, StateConnected, nil }
}

type recordSink struct {
	mu     sync.Mutex
	events []controls.Event
}

func (s *recordSink) Handle(ev controls.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *recordSink) snapshot() []controls.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]controls.Event(nil), s.events...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

var testMappings = []Mapping{
	{Usage: 0xB7, Action: "vfo_tune", Opts: map[string]any{"step": float64(10)}, Direction: 1},
	{Usage: 0xEA, Action: "vfo_tune", Opts: map[string]any{"step": float64(10)}, Direction: -1},
	{Usage: 0xE9, Action: "vfo_tune", Opts: map[string]any{"step": float64(10)}, Direction: -1},
	{Usage: 0xE2, Action: "vfo_swap"},
}

func startConnected(t *testing.T, sink Sink) (*Knob, *fakeTransport) {
	t.Helper()
	ft := newFakeTransport()
	k := newKnob(sink, testMappings, alwaysOpen(ft), 10*time.Millisecond)
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Close)
	waitFor(t, "connected", func() bool { return k.State() == StateConnected })
	return k, ft
}

// TestKnobDispatchesMappedReports: each mapped usage runs its action once,
// releases and unmapped usages run nothing.
func TestKnobDispatchesMappedReports(t *testing.T) {
	sink := &recordSink{}
	_, ft := startConnected(t, sink)

	for _, p := range [][]byte{
		{0x03, 0xb7, 0x00}, {0x03, 0x00, 0x00}, // turn right + release
		{0x03, 0xea, 0x00}, {0x03, 0x00, 0x00}, // turn left + release
		{0x03, 0xcd, 0x00}, {0x03, 0x00, 0x00}, // long press: unmapped here
		{0x03, 0xe2, 0x00}, {0x03, 0x00, 0x00}, // press + release
	} {
		ft.reports <- p
	}
	waitFor(t, "3 events", func() bool { return len(sink.snapshot()) >= 3 })
	time.Sleep(20 * time.Millisecond) // no extra events should follow

	got := sink.snapshot()
	want := []controls.Event{{Action: "vfo_tune", Value: 1}, {Action: "vfo_tune", Value: 127}, {Action: "vfo_swap", Value: 127}}
	if len(got) != len(want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Action != want[i].Action || got[i].Value != want[i].Value {
			t.Fatalf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDoubleReportIsTwoSteps: the knob sends press + turn left twice per click;
// each report is one step.
func TestDoubleReportIsTwoSteps(t *testing.T) {
	sink := &recordSink{}
	_, ft := startConnected(t, sink)

	ft.reports <- []byte{0x03, 0xe9, 0x00}
	ft.reports <- []byte{0x03, 0x00, 0x00}
	ft.reports <- []byte{0x03, 0xe9, 0x00}
	ft.reports <- []byte{0x03, 0x00, 0x00}
	waitFor(t, "2 events", func() bool { return len(sink.snapshot()) == 2 })
	for _, ev := range sink.snapshot() {
		if ev.Action != "vfo_tune" || ev.Value != 127 {
			t.Fatalf("event = %+v, want vfo_tune value 127", ev)
		}
	}
}

// TestLearnCapturesNextUsageWithoutDispatch: the gesture being learned must not
// also run its current action; the next one does.
func TestLearnCapturesNextUsageWithoutDispatch(t *testing.T) {
	sink := &recordSink{}
	k, ft := startConnected(t, sink)

	type result struct {
		usage uint16
		err   error
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		u, err := k.Learn(ctx)
		done <- result{u, err}
	}()
	waitFor(t, "learner registered", func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		return k.learner != nil
	})

	ft.reports <- []byte{0x03, 0xe2, 0x00}
	r := <-done
	if r.err != nil || r.usage != 0xE2 {
		t.Fatalf("Learn = 0x%04X, %v; want 0x00E2, nil", r.usage, r.err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(sink.snapshot()); n != 0 {
		t.Fatalf("learned gesture dispatched %d event(s), want 0", n)
	}

	ft.reports <- []byte{0x03, 0xe2, 0x00}
	waitFor(t, "dispatch after learning", func() bool { return len(sink.snapshot()) == 1 })
}

func TestLearnTimesOutAndIsReusable(t *testing.T) {
	k, ft := startConnected(t, &recordSink{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := k.Learn(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Learn error = %v, want context.DeadlineExceeded", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	go func() {
		time.Sleep(20 * time.Millisecond)
		ft.reports <- []byte{0x03, 0xb7, 0x00}
	}()
	if u, err := k.Learn(ctx2); err != nil || u != 0xB7 {
		t.Fatalf("second Learn = 0x%04X, %v; want 0x00B7, nil", u, err)
	}
}

func TestLearnBusy(t *testing.T) {
	k, _ := startConnected(t, &recordSink{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go k.Learn(ctx)
	waitFor(t, "learner registered", func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		return k.learner != nil
	})
	if _, err := k.Learn(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second concurrent Learn error = %v, want ErrBusy", err)
	}
}

// waitUntil polls cond until it is true or timeout elapses, returning whether
// it became true. Unlike waitFor, it does not fail the test — callers use it
// to count outcomes across many racy iterations.
func waitUntil(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

// TestLearnNeverLosesAGesture races a very short Learn timeout against a
// report arriving at about the same moment, many times. Every report sent
// must end up either returned by Learn or dispatched to the sink — never
// both, and never neither.
func TestLearnNeverLosesAGesture(t *testing.T) {
	sink := &recordSink{}
	k, ft := startConnected(t, sink)

	const iterations = 300
	var returned, dispatched int
	for i := 0; i < iterations; i++ {
		before := len(sink.snapshot())

		type learnResult struct {
			usage uint16
			err   error
		}
		resultCh := make(chan learnResult, 1)
		go func() {
			// A very short timeout puts ctx.Done() and the report's arrival
			// at handleReport within nanoseconds of each other, so the
			// unlock-then-send window in handleReport actually gets raced.
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Microsecond)
			defer cancel()
			u, err := k.Learn(ctx)
			resultCh <- learnResult{u, err}
		}()

		ft.reports <- []byte{0x03, 0xe2, 0x00}

		r := <-resultCh
		if r.err == nil {
			if r.usage != 0xE2 {
				t.Fatalf("iteration %d: Learn returned usage 0x%04X, want 0x00E2", i, r.usage)
			}
			returned++
			continue
		}
		if waitUntil(func() bool { return len(sink.snapshot()) > before }, 200*time.Millisecond) {
			dispatched++
		}
	}

	if total := returned + dispatched; total != iterations {
		t.Fatalf("returned=%d dispatched=%d total=%d, want %d (a report was lost)", returned, dispatched, total, iterations)
	}
	if got := len(sink.snapshot()); got != dispatched {
		t.Fatalf("sink has %d events, want %d (== dispatched)", got, dispatched)
	}
}

// TestKnobReportsStateAndRetries: a knob that isn't set up, then isn't
// connected, then appears, reports each state and connects without a restart.
func TestKnobReportsStateAndRetries(t *testing.T) {
	steps := make(chan openResult)
	k := newKnob(&recordSink{}, testMappings, stepOpener(steps), 5*time.Millisecond)
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}

	steps <- openResult{nil, StateNotSetUp, ErrNotSetUp}
	waitFor(t, "not_set_up", func() bool { return k.State() == StateNotSetUp })
	steps <- openResult{nil, StateNotConnected, ErrNotConnected}
	waitFor(t, "not_connected", func() bool { return k.State() == StateNotConnected })
	ft := newFakeTransport()
	steps <- openResult{ft, StateConnected, nil}
	waitFor(t, "connected", func() bool { return k.State() == StateConnected })
	k.Close()
}

// TestKnobReconnectsAfterReadError: unplugging mid-session closes the handle,
// reports not_connected, and the knob works again once reopened.
func TestKnobReconnectsAfterReadError(t *testing.T) {
	sink := &recordSink{}
	steps := make(chan openResult)
	k := newKnob(sink, testMappings, stepOpener(steps), 5*time.Millisecond)
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}

	first := newFakeTransport()
	steps <- openResult{first, StateConnected, nil}
	waitFor(t, "connected", func() bool { return k.State() == StateConnected })

	first.fail <- errors.New("device gone")
	waitFor(t, "not_connected after unplug", func() bool { return k.State() == StateNotConnected })
	if !first.isClosed() {
		t.Fatal("transport not closed after a read error")
	}

	second := newFakeTransport()
	steps <- openResult{second, StateConnected, nil}
	waitFor(t, "reconnected", func() bool { return k.State() == StateConnected })
	second.reports <- []byte{0x03, 0xb7, 0x00}
	waitFor(t, "event after reconnect", func() bool { return len(sink.snapshot()) == 1 })
	k.Close()
}

func TestCloseStopsReaderAndClosesTransport(t *testing.T) {
	ft := newFakeTransport()
	k := newKnob(&recordSink{}, testMappings, alwaysOpen(ft), 10*time.Millisecond)
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { return k.State() == StateConnected })

	done := make(chan struct{})
	go func() {
		k.Close()
		k.Close() // idempotent
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not return")
	}
	if !ft.isClosed() {
		t.Fatal("transport not closed by Close")
	}
}

func TestCloseWithoutStart(t *testing.T) {
	k := newKnob(&recordSink{}, testMappings, alwaysOpen(newFakeTransport()), 10*time.Millisecond)
	done := make(chan struct{})
	go func() { k.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close without Start did not return")
	}
}

func TestProbe(t *testing.T) {
	ft := newFakeTransport()
	if got := probe(alwaysOpen(ft)); got != StateConnected {
		t.Errorf("probe(present) = %q, want connected", got)
	}
	if !ft.isClosed() {
		t.Error("probe left the transport open")
	}
	notSetUp := func() (transport, State, error) { return nil, StateNotSetUp, ErrNotSetUp }
	if got := probe(notSetUp); got != StateNotSetUp {
		t.Errorf("probe(not set up) = %q, want not_set_up", got)
	}
}

func TestLearnOnce(t *testing.T) {
	ft := newFakeTransport()
	ft.reports <- []byte{0x03, 0x00, 0x00}
	ft.reports <- []byte{0x03, 0xcd, 0x00}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	u, st, err := learnOnce(ctx, alwaysOpen(ft))
	if err != nil || u != 0xCD || st != StateConnected {
		t.Fatalf("learnOnce = 0x%04X, %q, %v; want 0x00CD, connected, nil", u, st, err)
	}
	if !ft.isClosed() {
		t.Error("learnOnce left the transport open")
	}

	missing := func() (transport, State, error) { return nil, StateNotConnected, ErrNotConnected }
	if _, st, err := learnOnce(ctx, missing); st != StateNotConnected || !errors.Is(err, ErrNotConnected) {
		t.Errorf("learnOnce(missing) = %q, %v; want not_connected, ErrNotConnected", st, err)
	}

	short, cancelShort := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelShort()
	if _, _, err := learnOnce(short, alwaysOpen(newFakeTransport())); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("learnOnce(no movement) error = %v, want context.DeadlineExceeded", err)
	}
}

// TestStartUnsupported: on a platform without WinUSB, Start says so and the
// state is unsupported.
func TestStartUnsupported(t *testing.T) {
	if supported {
		t.Skip("this platform can read the knob")
	}
	k := New(&recordSink{}, testMappings)
	if err := k.Start(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Start error = %v, want ErrUnsupported", err)
	}
	if k.State() != StateUnsupported {
		t.Fatalf("State = %q, want unsupported", k.State())
	}
	k.Close()
}
