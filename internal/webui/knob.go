package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ujay/barncat-sparks/internal/config"
	"github.com/ujay/barncat-sparks/internal/hidknob"
)

// knobRunner is the part of *hidknob.Knob the web UI uses.
type knobRunner interface {
	Start() error
	State() hidknob.State
	SetTrace(on bool)
	Learn(ctx context.Context) (uint16, error)
	Close()
}

// Seams replaced in tests.
var (
	newKnob = func(sink hidknob.Sink, maps []hidknob.Mapping) knobRunner {
		return hidknob.New(sink, maps)
	}
	probeKnob     = hidknob.Probe
	learnKnobOnce = hidknob.LearnOnce
)

const (
	knobLearnTimeout = 10 * time.Second
	// knobFirstOpenWait bounds how long Start waits for the reader's first
	// attempt, so the status text names the real state.
	knobFirstOpenWait = 300 * time.Millisecond

	knobMsgNoMovement   = "No knob movement in 10 seconds"
	knobMsgNotSetUp     = "Knob found but not set up — replace its driver with WinUSB using Zadig"
	knobMsgNotConnected = "Knob not connected"
	knobMsgUnsupported  = "The knob requires Windows"
	knobMsgBusy         = "Already learning"
)

func writeJSONCode(rw http.ResponseWriter, code int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	json.NewEncoder(rw).Encode(v)
}

// startKnobLocked starts the knob reader on w.exec and returns the text to
// append to the running status. Must be called with w.mu held and w.exec set.
func (w *WebUI) startKnobLocked() string {
	maps, skipped := w.config.ToKnobMappings()
	for _, reason := range skipped {
		log.Printf("knob: ignored %s", reason)
	}

	k := newKnob(w.exec, maps)
	k.SetTrace(w.config.ProtocolTrace)
	var text string
	if err := k.Start(); err != nil {
		k.Close()
		text = ", Knob: requires Windows"
	} else {
		w.knob = k
		deadline := time.Now().Add(knobFirstOpenWait)
		for k.State() == hidknob.StateNotConnected && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		switch k.State() {
		case hidknob.StateConnected:
			text = ", Knob"
		case hidknob.StateNotSetUp:
			text = ", Knob: not set up (Zadig)"
		default:
			text = ", Knob: not connected (retrying)"
		}
	}

	switch n := len(skipped); {
	case n == 1:
		text += ", 1 knob row ignored"
	case n > 1:
		text += fmt.Sprintf(", %d knob rows ignored", n)
	}
	return text
}

// handleKnobStatus reports the knob's state: from the running knob while
// Started, otherwise from a one-time probe.
func (w *WebUI) handleKnobStatus(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.mu.Lock()
	k := w.knob
	w.mu.Unlock()
	var st hidknob.State
	if k != nil {
		st = k.State()
	} else {
		st = probeKnob()
	}
	writeJSON(rw, map[string]string{"state": string(st)})
}

// handleKnobLearn waits up to 10 seconds for the next knob gesture and returns
// its usage code and name.
func (w *WebUI) handleKnobLearn(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireJSONContentType(rw, r) {
		return
	}
	if !w.knobLearnMu.TryLock() {
		writeJSONCode(rw, http.StatusTooManyRequests, map[string]string{"error": knobMsgBusy})
		return
	}
	defer w.knobLearnMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), knobLearnTimeout)
	defer cancel()

	w.mu.Lock()
	k := w.knob
	w.mu.Unlock()

	var (
		usage uint16
		st    hidknob.State
		err   error
	)
	if k != nil {
		st = k.State()
		if st == hidknob.StateConnected {
			usage, err = k.Learn(ctx)
		} else {
			err = hidknob.ErrNotConnected
		}
	} else {
		usage, st, err = learnKnobOnce(ctx)
	}
	if err != nil {
		code, msg := knobLearnFailure(st, err)
		writeJSONCode(rw, code, map[string]string{"error": msg})
		return
	}
	name := config.KnobControlNames[fmt.Sprintf("knob:%d:%d", config.KnobReportID, usage)]
	writeJSON(rw, map[string]any{"usage": usage, "name": name})
}

// knobLearnFailure maps a failed Learn to its HTTP status and message.
func knobLearnFailure(st hidknob.State, err error) (int, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, knobMsgNoMovement
	case errors.Is(err, hidknob.ErrBusy):
		return http.StatusTooManyRequests, knobMsgBusy
	case st == hidknob.StateNotSetUp || errors.Is(err, hidknob.ErrNotSetUp):
		return http.StatusConflict, knobMsgNotSetUp
	case st == hidknob.StateUnsupported || errors.Is(err, hidknob.ErrUnsupported):
		return http.StatusNotImplemented, knobMsgUnsupported
	default:
		return http.StatusNotFound, knobMsgNotConnected
	}
}
