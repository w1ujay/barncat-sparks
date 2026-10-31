package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
)

// fakeRadio answers each poll with the next error fed to it.
type fakeRadio struct {
	replies chan error
	done    chan struct{}
}

func newFakeRadio() *fakeRadio {
	return &fakeRadio{replies: make(chan error), done: make(chan struct{})}
}

func (f *fakeRadio) GetFreqA() (int, error) {
	select {
	case err := <-f.replies:
		return 14025000, err
	case <-f.done:
		return 0, errors.New("link closed")
	}
}

func (f *fakeRadio) Done() <-chan struct{} { return f.done }

// reply answers one poll, and returns once the watcher has taken it in.
func (f *fakeRadio) reply(t *testing.T, err error) {
	t.Helper()
	select {
	case f.replies <- err:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher stopped polling")
	}
}

func waitForRadio(t *testing.T, w *WebUI, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := w.radioLink(); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("radio = %q, want %q", w.radioLink(), want)
}

func startWatch(t *testing.T, w *WebUI, r radioProbe) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	exited := make(chan struct{})
	w.running = true
	w.setRadioLink(radioChecking)
	go func() {
		w.watchRadio(ctx, r, time.Millisecond)
		close(exited)
	}()
	return cancel, exited
}

func TestRadioConnectedOnReply(t *testing.T) {
	w, f := &WebUI{}, newFakeRadio()
	startWatch(t, w, f)
	if got := w.radioLink(); got != radioChecking {
		t.Fatalf("before the first reply: %q", got)
	}
	f.reply(t, nil)
	waitForRadio(t, w, radioConnected)
}

func TestRadioSurvivesOneMissedPoll(t *testing.T) {
	w, f := &WebUI{}, newFakeRadio()
	startWatch(t, w, f)
	f.reply(t, nil)
	f.reply(t, errors.New("timeout"))
	f.reply(t, nil) // this poll only starts once the miss has been counted
	waitForRadio(t, w, radioConnected)
}

func TestRadioNotRespondingThenRecovers(t *testing.T) {
	w, f := &WebUI{}, newFakeRadio()
	startWatch(t, w, f)
	f.reply(t, nil)
	f.reply(t, errors.New("timeout"))
	f.reply(t, errors.New("timeout"))
	waitForRadio(t, w, radioNoReply)
	f.reply(t, nil)
	waitForRadio(t, w, radioConnected)
}

func TestRadioLinkLostStopsWatching(t *testing.T) {
	w, f := &WebUI{}, newFakeRadio()
	_, exited := startWatch(t, w, f)
	f.reply(t, nil)
	close(f.done)
	waitForRadio(t, w, radioLost)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher kept running")
	}
}

func TestRadioWatchEndsOnStop(t *testing.T) {
	w, f := &WebUI{}, newFakeRadio()
	cancel, exited := startWatch(t, w, f)
	f.reply(t, nil)
	waitForRadio(t, w, radioConnected)
	cancel()
	close(f.done) // Stop closes the link too; that isn't a lost link
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher kept running")
	}
	if got := w.radioLink(); got != radioConnected {
		t.Fatalf("Stop was reported as %q", got)
	}
}

// TestRadioWatchOverRealCATClient runs the watcher on a real client: a radio
// that answers, then goes quiet with the link open, then drops the link.
func TestRadioWatchOverRealCATClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var silent atomic.Bool
	conns := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conns <- conn
		buf := make([]byte, 256)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			if !silent.Load() && strings.Contains(string(buf[:n]), "FA;") {
				conn.Write([]byte("FA00014025000;"))
			}
		}
	}()

	client := cat.NewCATClient()
	if err := client.ConnectTCP(ln.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	w := &WebUI{}
	startWatch(t, w, client)
	waitForRadio(t, w, radioConnected)
	silent.Store(true)
	waitForRadio(t, w, radioNoReply)
	silent.Store(false)
	waitForRadio(t, w, radioConnected)
	(<-conns).Close()
	waitForRadio(t, w, radioLost)
}

func statusOf(t *testing.T, w *WebUI) statusResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	w.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var s statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStatusReportsRadio(t *testing.T) {
	w := &WebUI{}
	if got := statusOf(t, w).Radio; got != radioOff {
		t.Fatalf("stopped: radio = %q", got)
	}
	w.running = true
	w.setRadioLink(radioNoReply)
	if got := statusOf(t, w).Radio; got != radioNoReply {
		t.Fatalf("running: radio = %q", got)
	}
}

func TestStopEndsRadioWatch(t *testing.T) {
	w := &WebUI{running: true}
	w.setRadioLink(radioConnected)
	ended := false
	w.radioCancel = func() { ended = true }
	w.mu.Lock()
	w.stopLocked()
	w.mu.Unlock()
	if !ended {
		t.Fatal("Stop left the watcher running")
	}
	if got := statusOf(t, w).Radio; got != radioOff {
		t.Fatalf("after Stop: radio = %q", got)
	}
}

func TestIndexHearthIsOptional(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="radioLink"`, `function renderRadio(`, `s.radio`,
		`<div id="stationSection" hidden>`, `getElementById('stationSection').hidden = !url`,
		`<details id="hearthDetails">`, `getElementById('hearthDetails').open`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
	for _, gone := range []string{`stationHint`, `No hearth configured`} {
		if strings.Contains(body, gone) {
			t.Errorf("webui.html still has %s", gone)
		}
	}
}
