package webui

import (
	"context"
	"time"
)

const (
	radioPollInterval = 5 * time.Second
	radioMaxMisses    = 2 // one slow reply isn't worth alarming the operator
)

// What the Operate tab says about the radio.
const (
	radioOff       = "off"
	radioChecking  = "checking"
	radioConnected = "connected"
	radioNoReply   = "no_reply"
	radioLost      = "lost"
)

// radioProbe is the part of the CAT client the watcher needs.
type radioProbe interface {
	GetFreqA() (int, error)
	Done() <-chan struct{}
}

func (w *WebUI) setRadioLink(state string) { w.radioState.Store(state) }

// radioLink reports the radio's state. Stopped, there is no link to report.
func (w *WebUI) radioLink() string {
	if s, ok := w.radioState.Load().(string); ok {
		return s
	}
	return radioOff
}

// statusRadioLocked is the state for a status reply. Call with w.mu held.
func (w *WebUI) statusRadioLocked() string {
	if !w.running {
		return radioOff
	}
	return w.radioLink()
}

// watchRadio asks the radio for its frequency until ctx ends or the link does.
// An open link proves little: a serial port or a remote server stays open with
// the radio turned off, so only a reply counts as connected.
func (w *WebUI) watchRadio(ctx context.Context, r radioProbe, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	misses := 0
	for {
		_, err := r.GetFreqA()
		if ctx.Err() != nil {
			return
		}
		select {
		case <-r.Done():
			w.setRadioLink(radioLost)
			return
		default:
		}
		if err == nil {
			misses = 0
			w.setRadioLink(radioConnected)
		} else if misses++; misses >= radioMaxMisses {
			w.setRadioLink(radioNoReply)
		}

		select {
		case <-ctx.Done():
			return
		case <-r.Done():
			if ctx.Err() == nil {
				w.setRadioLink(radioLost)
			}
			return
		case <-ticker.C:
		}
	}
}
