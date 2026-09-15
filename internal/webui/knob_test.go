package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ujay/barncat-sparks/internal/cat"
	"github.com/ujay/barncat-sparks/internal/config"
	"github.com/ujay/barncat-sparks/internal/controls"
	"github.com/ujay/barncat-sparks/internal/hidknob"
)

type fakeKnob struct {
	state      hidknob.State
	startErr   error
	learnUsage uint16
	learnErr   error

	started, closed, trace bool
	learned                int
	maps                   []hidknob.Mapping
	onClose                func()
}

func (f *fakeKnob) Start() error         { f.started = true; return f.startErr }
func (f *fakeKnob) State() hidknob.State { return f.state }
func (f *fakeKnob) SetTrace(on bool)     { f.trace = on }
func (f *fakeKnob) Close() {
	f.closed = true
	if f.onClose != nil {
		f.onClose()
	}
}
func (f *fakeKnob) Learn(ctx context.Context) (uint16, error) {
	f.learned++
	return f.learnUsage, f.learnErr
}

// withSeams replaces the knob constructor and one-shot helpers for one test.
func withSeams(t *testing.T, fk *fakeKnob, probe func() hidknob.State, once func(context.Context) (uint16, hidknob.State, error)) {
	t.Helper()
	oldNew, oldProbe, oldOnce := newKnob, probeKnob, learnKnobOnce
	t.Cleanup(func() { newKnob, probeKnob, learnKnobOnce = oldNew, oldProbe, oldOnce })
	if fk != nil {
		newKnob = func(sink hidknob.Sink, maps []hidknob.Mapping) knobRunner { fk.maps = maps; return fk }
	}
	if probe != nil {
		probeKnob = probe
	}
	if once != nil {
		learnKnobOnce = once
	}
}

func knobWebUI() *WebUI {
	return &WebUI{config: &config.AppConfig{}, statusMsg: "Stopped"}
}

func TestKnobStatusWhileStoppedProbes(t *testing.T) {
	withSeams(t, nil, func() hidknob.State { return hidknob.StateNotSetUp }, nil)
	code, out := callJSON(t, knobWebUI().handleKnobStatus, http.MethodGet, "")
	if code != http.StatusOK || out["state"] != "not_set_up" {
		t.Fatalf("status = %d %v, want 200 state not_set_up", code, out)
	}
}

func TestKnobStatusWhileRunningAsksTheKnob(t *testing.T) {
	withSeams(t, nil, func() hidknob.State { t.Fatal("probed while the knob is running"); return "" }, nil)
	w := knobWebUI()
	w.knob = &fakeKnob{state: hidknob.StateConnected}
	if _, out := callJSON(t, w.handleKnobStatus, http.MethodGet, ""); out["state"] != "connected" {
		t.Fatalf("state = %v, want connected", out["state"])
	}
}

func TestKnobStatusRejectsPost(t *testing.T) {
	if code, _ := callJSON(t, knobWebUI().handleKnobStatus, http.MethodPost, "{}"); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/knob/status = %d, want 405", code)
	}
}

func TestKnobLearnWhileStopped(t *testing.T) {
	withSeams(t, nil, nil, func(context.Context) (uint16, hidknob.State, error) {
		return 183, hidknob.StateConnected, nil
	})
	code, out := callJSON(t, knobWebUI().handleKnobLearn, http.MethodPost, "{}")
	if code != http.StatusOK || out["usage"] != float64(183) || out["name"] != "Turn Right" {
		t.Fatalf("learn = %d %v, want 200 usage 183 name Turn Right", code, out)
	}
}

func TestKnobLearnWhileRunning(t *testing.T) {
	w := knobWebUI()
	fk := &fakeKnob{state: hidknob.StateConnected, learnUsage: 205}
	w.knob = fk
	code, out := callJSON(t, w.handleKnobLearn, http.MethodPost, "{}")
	if code != http.StatusOK || out["usage"] != float64(205) || out["name"] != "Long Press" {
		t.Fatalf("learn = %d %v, want 200 usage 205 name Long Press", code, out)
	}

	notSetUp := &fakeKnob{state: hidknob.StateNotSetUp}
	w.knob = notSetUp
	code, out = callJSON(t, w.handleKnobLearn, http.MethodPost, "{}")
	if code != http.StatusConflict || notSetUp.learned != 0 {
		t.Fatalf("learn on a not-set-up knob = %d %v (Learn called %d times), want 409 without calling Learn", code, out, notSetUp.learned)
	}
}

func TestKnobLearnFailures(t *testing.T) {
	tests := []struct {
		name  string
		state hidknob.State
		err   error
		code  int
		msg   string
	}{
		{"no movement", hidknob.StateConnected, context.DeadlineExceeded, http.StatusRequestTimeout, "No knob movement in 10 seconds"},
		{"not set up", hidknob.StateNotSetUp, hidknob.ErrNotSetUp, http.StatusConflict, "Knob found but not set up — replace its driver with WinUSB using Zadig"},
		{"not connected", hidknob.StateNotConnected, hidknob.ErrNotConnected, http.StatusNotFound, "Knob not connected"},
		{"unsupported", hidknob.StateUnsupported, hidknob.ErrUnsupported, http.StatusNotImplemented, "The knob requires Windows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withSeams(t, nil, nil, func(context.Context) (uint16, hidknob.State, error) { return 0, tt.state, tt.err })
			code, out := callJSON(t, knobWebUI().handleKnobLearn, http.MethodPost, "{}")
			if code != tt.code || out["error"] != tt.msg {
				t.Fatalf("learn = %d %v, want %d %q", code, out, tt.code, tt.msg)
			}
		})
	}
}

func TestKnobLearnBusy(t *testing.T) {
	w := knobWebUI()
	w.knobLearnMu.Lock()
	defer w.knobLearnMu.Unlock()
	code, out := callJSON(t, w.handleKnobLearn, http.MethodPost, "{}")
	if code != http.StatusTooManyRequests || out["error"] != "Already learning" {
		t.Fatalf("learn while learning = %d %v, want 429 Already learning", code, out)
	}
}

func TestKnobLearnMethodAndContentType(t *testing.T) {
	w := knobWebUI()
	if code, _ := callJSON(t, w.handleKnobLearn, http.MethodGet, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/knob/learn = %d, want 405", code)
	}
	rec := httptest.NewRecorder()
	w.handleKnobLearn(rec, httptest.NewRequest(http.MethodPost, "/api/knob/learn", strings.NewReader("{}")))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST without Content-Type = %d, want 415", rec.Code)
	}
}

func knobRows() []config.MappingEntry {
	return []config.MappingEntry{
		{Action: "vfo_tune", Type: "knob", Channel: 3, Number: 183, Opts: map[string]any{"step": float64(10), "direction": float64(1)}},
		{Action: "ptt", Type: "knob", Channel: 3, Number: 226},
	}
}

func TestStartKnobLockedStatusText(t *testing.T) {
	tests := []struct {
		name string
		fk   *fakeKnob
		want string
	}{
		{"connected", &fakeKnob{state: hidknob.StateConnected}, ", Knob, 1 knob row ignored"},
		{"not set up", &fakeKnob{state: hidknob.StateNotSetUp}, ", Knob: not set up (Zadig), 1 knob row ignored"},
		{"not connected", &fakeKnob{state: hidknob.StateNotConnected}, ", Knob: not connected (retrying), 1 knob row ignored"},
		{"unsupported", &fakeKnob{state: hidknob.StateUnsupported, startErr: hidknob.ErrUnsupported}, ", Knob: requires Windows, 1 knob row ignored"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withSeams(t, tt.fk, nil, nil)
			w := &WebUI{config: &config.AppConfig{KnobEnabled: true, ProtocolTrace: true, Mappings: knobRows()}}
			w.exec = controls.New(nil)

			if got := w.startKnobLocked(); got != tt.want {
				t.Errorf("status text = %q, want %q", got, tt.want)
			}
			if !tt.fk.started || !tt.fk.trace || len(tt.fk.maps) != 1 {
				t.Errorf("knob started=%v trace=%v mappings=%d, want true true 1", tt.fk.started, tt.fk.trace, len(tt.fk.maps))
			}
			if tt.fk.startErr != nil && (w.knob != nil || !tt.fk.closed) {
				t.Errorf("a knob that failed to start must be closed and not kept (knob=%v closed=%v)", w.knob, tt.fk.closed)
			}
			if tt.fk.startErr == nil && w.knob != knobRunner(tt.fk) {
				t.Error("a started knob must be kept on the WebUI")
			}
		})
	}
}

func TestStopClosesKnob(t *testing.T) {
	fk := &fakeKnob{state: hidknob.StateConnected}
	w := knobWebUI()
	w.knob = fk
	w.knobNote = ", Knob: requires Windows"
	w.mu.Lock()
	w.stopLocked()
	w.mu.Unlock()
	if !fk.closed || w.knob != nil {
		t.Fatalf("after stop: closed=%v knob=%v, want closed and cleared", fk.closed, w.knob)
	}
	if w.knobNote != "" {
		t.Errorf("knobNote = %q after stop, want empty", w.knobNote)
	}
}

// TestStopClearsNoteFromFailedKnobStart: a knob that failed to start leaves
// w.knob nil but a note; a later Start with the knob off must not show it.
func TestStopClearsNoteFromFailedKnobStart(t *testing.T) {
	w := knobWebUI()
	w.knobNote = ", Knob: requires Windows"
	w.mu.Lock()
	w.stopLocked()
	w.mu.Unlock()
	if w.knobNote != "" {
		t.Errorf("knobNote = %q after stop, want empty", w.knobNote)
	}
}

// TestTraceReachesRunningKnob: the Protocol Trace checkbox turns knob tracing
// on and off while Started, including when no WinKeyer is running.
func TestTraceReachesRunningKnob(t *testing.T) {
	w := knobWebUI()
	w.running = true
	fk := &fakeKnob{state: hidknob.StateConnected}
	w.knob = fk

	if code, out := callJSON(t, w.handleTrace, http.MethodPost, `{"enabled":true}`); code != http.StatusOK || !fk.trace {
		t.Fatalf("trace on = %d %v, knob trace %v; want 200 and knob tracing", code, out, fk.trace)
	}
	if code, out := callJSON(t, w.handleTrace, http.MethodPost, `{"enabled":false}`); code != http.StatusOK || fk.trace {
		t.Fatalf("trace off = %d %v, knob trace %v; want 200 and knob not tracing", code, out, fk.trace)
	}
}

// TestStatusFollowsKnobState: /api/status reads the knob's live state each
// time, not a string frozen at Start.
func TestStatusFollowsKnobState(t *testing.T) {
	w := knobWebUI()
	w.running = true
	w.statusMsg = "Running — Radio: k4:9200 (TCP)"
	w.knobNote = ", 1 knob row ignored"
	fk := &fakeKnob{state: hidknob.StateConnected}
	w.knob = fk

	_, out := callJSON(t, w.handleStatus, http.MethodGet, "")
	want := "Running — Radio: k4:9200 (TCP), Knob, 1 knob row ignored"
	if out["message"] != want {
		t.Fatalf("message = %q, want %q", out["message"], want)
	}

	fk.state = hidknob.StateNotConnected
	_, out = callJSON(t, w.handleStatus, http.MethodGet, "")
	want = "Running — Radio: k4:9200 (TCP), Knob: not connected (retrying), 1 knob row ignored"
	if out["message"] != want {
		t.Fatalf("message = %q, want %q", out["message"], want)
	}
}

// pttLogRadio appends "ptt on"/"ptt off" to a shared log on SetPTT. Any other
// Radio method panics (nil embedded interface), so an unexpected call fails
// loudly.
type pttLogRadio struct {
	cat.Radio
	log func(string)
}

func (r *pttLogRadio) SetPTT(on bool) error {
	if on {
		r.log("ptt on")
	} else {
		r.log("ptt off")
	}
	return nil
}

// TestStopReleasesPTTAfterClosingInputs pins the order stopLocked must
// follow: close the knob (dropping a held PTT's only source of a release)
// before the executor releases it, so the radio is never left transmitting.
func TestStopReleasesPTTAfterClosingInputs(t *testing.T) {
	var mu sync.Mutex
	var events []string
	logEvent := func(s string) {
		mu.Lock()
		events = append(events, s)
		mu.Unlock()
	}

	radio := &pttLogRadio{log: logEvent}
	w := knobWebUI()
	w.exec = controls.New(radio)
	w.exec.Handle(controls.Event{Action: "ptt", Value: 127})
	w.knob = &fakeKnob{onClose: func() { logEvent("knob closed") }}

	w.mu.Lock()
	w.stopLocked()
	w.mu.Unlock()

	want := []string{"ptt on", "knob closed", "ptt off"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if w.exec != nil {
		t.Error("w.exec not nil after stop")
	}
}

func TestControlNamesIncludeKnob(t *testing.T) {
	rec := httptest.NewRecorder()
	knobWebUI().handleControlNames(rec, httptest.NewRequest(http.MethodGet, "/api/control-names", nil))
	var names map[string]map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&names); err != nil {
		t.Fatal(err)
	}
	if names["knob"]["knob:3:183"] != "Turn Right" {
		t.Errorf(`names["knob"]["knob:3:183"] = %q, want Turn Right`, names["knob"]["knob:3:183"])
	}
	if names["DJ2GO2"]["cc:0:6"] != "Jog Wheel A" {
		t.Errorf("DJ2GO2 names missing: %v", names["DJ2GO2"]["cc:0:6"])
	}
}
