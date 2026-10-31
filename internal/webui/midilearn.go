package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ujay/barncat-sparks/internal/midi"
)

// midiLearner is the part of *midi.MIDIDriver the web UI uses to learn.
type midiLearner interface {
	Learn(ctx context.Context) (midi.Learned, error)
}

// Seams replaced in tests.
var (
	learnMIDIOnce      = midi.LearnOnce
	currentMIDILearner = func(w *WebUI) midiLearner {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.midiDrv == nil {
			return nil
		}
		return w.midiDrv
	}
)

const (
	midiMsgNoMovement   = "No MIDI control moved in 10 seconds"
	midiMsgNotConnected = "MIDI controller not found — check it is plugged in and no other program is using it"
	midiMsgNoDevice     = "Tick a MIDI device first"
	midiMsgUnsupported  = "MIDI requires Windows"
	midiMsgBusy         = "Already learning"
)

// handleMIDILearn waits up to 10 seconds for the next press or move on the
// MIDI controller and returns its type, channel and number. While running it
// asks the driver, which already holds the devices; while stopped it opens the
// ticked devices just for this Learn.
func (w *WebUI) handleMIDILearn(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireJSONContentType(rw, r) {
		return
	}
	var req struct {
		Devices []string `json:"devices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if !w.midiLearnMu.TryLock() {
		writeJSONCode(rw, http.StatusTooManyRequests, map[string]string{"error": midiMsgBusy})
		return
	}
	defer w.midiLearnMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), knobLearnTimeout)
	defer cancel()

	var (
		got midi.Learned
		err error
	)
	if l := currentMIDILearner(w); l != nil {
		got, err = l.Learn(ctx)
	} else {
		devices := req.Devices
		if len(devices) == 0 {
			w.mu.Lock()
			devices = w.config.GetMIDIDevices()
			w.mu.Unlock()
		}
		if len(devices) == 0 {
			writeJSONCode(rw, http.StatusConflict, map[string]string{"error": midiMsgNoDevice})
			return
		}
		got, err = learnMIDIOnce(ctx, devices)
	}
	if err != nil {
		code, msg := midiLearnFailure(err)
		writeJSONCode(rw, code, map[string]string{"error": msg})
		return
	}
	writeJSON(rw, got)
}

// midiLearnFailure maps a failed Learn to its HTTP status and message.
func midiLearnFailure(err error) (int, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, midiMsgNoMovement
	case errors.Is(err, midi.ErrBusy):
		return http.StatusTooManyRequests, midiMsgBusy
	case errors.Is(err, midi.ErrUnsupported):
		return http.StatusNotImplemented, midiMsgUnsupported
	default:
		return http.StatusNotFound, midiMsgNotConnected
	}
}
