package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ujay/barncat-sparks/internal/config"
	"github.com/ujay/barncat-sparks/internal/midi"
)

type fakeMIDILearner struct {
	got midi.Learned
	err error
}

func (f *fakeMIDILearner) Learn(context.Context) (midi.Learned, error) { return f.got, f.err }

// withMIDISeams replaces the running driver and the stopped-state Learn.
func withMIDISeams(t *testing.T, running midiLearner, once func(context.Context, []string) (midi.Learned, error)) {
	t.Helper()
	oldRunning, oldOnce := currentMIDILearner, learnMIDIOnce
	t.Cleanup(func() { currentMIDILearner, learnMIDIOnce = oldRunning, oldOnce })
	currentMIDILearner = func(*WebUI) midiLearner { return running }
	learnMIDIOnce = once
}

func TestMIDILearnWhileRunningUsesDriver(t *testing.T) {
	withMIDISeams(t, &fakeMIDILearner{got: midi.Learned{Type: "note_on", Channel: 1, Number: 7}},
		func(context.Context, []string) (midi.Learned, error) {
			t.Fatal("LearnOnce must not open a device the driver already holds")
			return midi.Learned{}, nil
		})
	code, out := callJSON(t, knobWebUI().handleMIDILearn, http.MethodPost, `{"devices":["DJ2GO2"]}`)
	if code != http.StatusOK || out["type"] != "note_on" || out["channel"] != 1.0 || out["number"] != 7.0 {
		t.Fatalf("learn = %d %v, want 200 note_on ch 1 number 7", code, out)
	}
}

func TestMIDILearnWhileStoppedUsesPageDevices(t *testing.T) {
	var opened []string
	withMIDISeams(t, nil, func(_ context.Context, devices []string) (midi.Learned, error) {
		opened = devices
		return midi.Learned{Type: "cc", Channel: 0, Number: 22}, nil
	})
	w := &WebUI{config: &config.AppConfig{MIDIDevices: []string{"Saved"}}}
	code, out := callJSON(t, w.handleMIDILearn, http.MethodPost, `{"devices":["DJ2GO2"]}`)
	if code != http.StatusOK || out["type"] != "cc" || out["number"] != 22.0 {
		t.Fatalf("learn = %d %v, want 200 cc 22", code, out)
	}
	if !reflect.DeepEqual(opened, []string{"DJ2GO2"}) {
		t.Fatalf("opened %v, want the devices ticked on the page", opened)
	}
}

func TestMIDILearnFallsBackToSavedDevices(t *testing.T) {
	var opened []string
	withMIDISeams(t, nil, func(_ context.Context, devices []string) (midi.Learned, error) {
		opened = devices
		return midi.Learned{Type: "cc"}, nil
	})
	w := &WebUI{config: &config.AppConfig{MIDIDevices: []string{"Saved"}}}
	if code, out := callJSON(t, w.handleMIDILearn, http.MethodPost, `{}`); code != http.StatusOK {
		t.Fatalf("learn = %d %v, want 200", code, out)
	}
	if !reflect.DeepEqual(opened, []string{"Saved"}) {
		t.Fatalf("opened %v, want the saved devices", opened)
	}
}

func TestMIDILearnNoDevice(t *testing.T) {
	withMIDISeams(t, nil, func(context.Context, []string) (midi.Learned, error) {
		t.Fatal("nothing to open")
		return midi.Learned{}, nil
	})
	code, out := callJSON(t, knobWebUI().handleMIDILearn, http.MethodPost, `{}`)
	if code != http.StatusConflict || out["error"] != midiMsgNoDevice {
		t.Fatalf("learn = %d %v, want 409 %q", code, out, midiMsgNoDevice)
	}
}

func TestMIDILearnFailures(t *testing.T) {
	tests := []struct {
		err  error
		code int
		msg  string
	}{
		{context.DeadlineExceeded, http.StatusRequestTimeout, midiMsgNoMovement},
		{midi.ErrNotConnected, http.StatusNotFound, midiMsgNotConnected},
		{midi.ErrUnsupported, http.StatusNotImplemented, midiMsgUnsupported},
		{midi.ErrBusy, http.StatusTooManyRequests, midiMsgBusy},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			withMIDISeams(t, &fakeMIDILearner{err: tt.err}, nil)
			code, out := callJSON(t, knobWebUI().handleMIDILearn, http.MethodPost, `{}`)
			if code != tt.code || out["error"] != tt.msg {
				t.Fatalf("learn = %d %v, want %d %q", code, out, tt.code, tt.msg)
			}
		})
	}
}

func TestMIDILearnBusy(t *testing.T) {
	w := knobWebUI()
	w.midiLearnMu.Lock()
	defer w.midiLearnMu.Unlock()
	code, out := callJSON(t, w.handleMIDILearn, http.MethodPost, `{}`)
	if code != http.StatusTooManyRequests || out["error"] != midiMsgBusy {
		t.Fatalf("learn while learning = %d %v, want 429", code, out)
	}
}

func TestMIDILearnMethodAndContentType(t *testing.T) {
	w := knobWebUI()
	if code, _ := callJSON(t, w.handleMIDILearn, http.MethodGet, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", code)
	}
	rec := httptest.NewRecorder()
	w.handleMIDILearn(rec, httptest.NewRequest(http.MethodPost, "/api/midi/learn", strings.NewReader("{}")))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST without JSON content type = %d, want 415", rec.Code)
	}
}

func TestMIDILearnBadBody(t *testing.T) {
	withMIDISeams(t, nil, nil)
	if code, _ := callJSON(t, knobWebUI().handleMIDILearn, http.MethodPost, `{`); code != http.StatusBadRequest {
		t.Errorf("bad body = %d, want 400", code)
	}
}

func TestCurrentMIDILearnerNilWhenStopped(t *testing.T) {
	if l := currentMIDILearner(knobWebUI()); l != nil {
		t.Fatalf("learner = %#v, want a true nil so the stopped path runs", l)
	}
}

// TestIndexWiresMIDILearn guards the Learn button on CC and Note On rows.
func TestIndexWiresMIDILearn(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`'/api/midi/learn'`, `function learnMIDI(`, `onclick="learnMIDI(`, `devices: midiDevices`, `row.type = data.type`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
}
