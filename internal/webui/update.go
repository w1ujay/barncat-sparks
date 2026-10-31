package webui

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/ujay/barncat-sparks/internal/config"
	"github.com/ujay/barncat-sparks/internal/update"
)

const updateCheckTimeout = 15 * time.Second

// updateStatus is the JSON shape of /api/update.
type updateStatus struct {
	Current   string `json:"current"`
	Checked   bool   `json:"checked"` // the startup check has finished, either way
	Available bool   `json:"available"`
	Latest    string `json:"latest,omitempty"`
	URL       string `json:"url,omitempty"`
}

// checkForUpdate asks GitHub once for the latest release. A failure is only
// logged: a station with no internet sees nothing at all.
func (w *WebUI) checkForUpdate(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	rel, err := w.updater.Latest(ctx)

	w.updateMu.Lock()
	defer w.updateMu.Unlock()
	w.updateChecked = true
	if err != nil {
		log.Printf("update: check failed: %v", err)
		return
	}
	if update.Newer(config.Version, rel.Version) {
		w.updateRelease = &rel
	}
}

// handleUpdateStatus reports the result of the startup check.
func (w *WebUI) handleUpdateStatus(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.updateMu.Lock()
	defer w.updateMu.Unlock()
	s := updateStatus{Current: config.Version, Checked: w.updateChecked}
	if w.updateRelease != nil {
		s.Available = true
		s.Latest = w.updateRelease.Version
		s.URL = w.updateRelease.URL
	}
	writeJSON(rw, s)
}

// handleUpdateOpen opens the release page in the default browser, since the
// app window can't browse. The URL is the one from the check, never the page's.
func (w *WebUI) handleUpdateOpen(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireJSONContentType(rw, r) {
		return
	}
	w.updateMu.Lock()
	rel := w.updateRelease
	w.updateMu.Unlock()
	if rel == nil {
		http.Error(rw, "no update available", http.StatusNotFound)
		return
	}
	w.openURL(rel.URL)
	rw.WriteHeader(http.StatusNoContent)
}
