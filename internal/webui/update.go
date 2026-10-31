package webui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ujay/barncat-sparks/internal/config"
	"github.com/ujay/barncat-sparks/internal/update"
)

const (
	updateCheckTimeout   = 15 * time.Second
	updateInstallTimeout = 5 * time.Minute
	updateExitGrace      = 5 * time.Second // then exit anyway: the new copy is waiting
)

// Update progress, as shown on the page. "" means no update has been tried.
const (
	updateDownloading = "downloading"
	updateInstalling  = "installing"
	updateRestarting  = "restarting"
	updateFailed      = "failed"
)

// updateStatus is the JSON shape of /api/update.
type updateStatus struct {
	Current   string `json:"current"`
	Checked   bool   `json:"checked"` // the startup check has finished, either way
	Available bool   `json:"available"`
	Latest    string `json:"latest,omitempty"`
	URL       string `json:"url,omitempty"`

	CanInstall bool   `json:"can_install"` // this build can update itself
	State      string `json:"state,omitempty"`
	Error      string `json:"error,omitempty"`
}

// checkForUpdate asks GitHub once for the newest release the settings allow. A
// failure is only logged: a station with no internet sees nothing at all.
func (w *WebUI) checkForUpdate(ctx context.Context) {
	w.mu.Lock()
	prereleases := w.config != nil && w.config.UpdatePrereleases
	w.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	var rel update.Release
	var err error
	if prereleases {
		rel, err = w.updater.NewestIncludingPrereleases(ctx)
	} else {
		rel, err = w.updater.Latest(ctx)
	}

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

// handleUpdateStatus reports the update check and any update in progress.
func (w *WebUI) handleUpdateStatus(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.updateMu.Lock()
	defer w.updateMu.Unlock()
	s := updateStatus{
		Current:    config.Version,
		Checked:    w.updateChecked,
		CanInstall: w.selfUpdate,
		State:      w.updateState,
		Error:      w.updateErr,
	}
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

func (w *WebUI) setUpdateState(state, errText string) {
	w.updateMu.Lock()
	defer w.updateMu.Unlock()
	w.updateState = state
	w.updateErr = errText
}

// handleUpdateInstall starts the update and returns; the page follows it
// through /api/update. It is refused while running, and Start is refused from
// here on, so an update never closes Sparks in the middle of a QSO.
func (w *WebUI) handleUpdateInstall(rw http.ResponseWriter, r *http.Request) {
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
		writeJSONCode(rw, http.StatusNotFound, map[string]string{"error": "No update available"})
		return
	}
	if !w.selfUpdate {
		writeJSONCode(rw, http.StatusConflict, map[string]string{"error": "This build can't update itself"})
		return
	}
	exe, err := w.exePath()
	if err != nil {
		writeJSONCode(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		writeJSONCode(rw, http.StatusConflict, map[string]string{"error": "Stop Sparks before updating"})
		return
	}
	if w.updating {
		writeJSONCode(rw, http.StatusConflict, map[string]string{"error": "An update is already running"})
		return
	}
	w.updating = true
	w.setUpdateState(updateDownloading, "")
	go w.runUpdate(*rel, exe)
	writeJSONCode(rw, http.StatusAccepted, map[string]string{"status": "started"})
}

// runUpdate does the update and exits Sparks, or reports why it couldn't and
// lets Start and another try go ahead.
func (w *WebUI) runUpdate(rel update.Release, exe string) {
	if err := w.installUpdate(rel, exe); err != nil {
		log.Printf("update: %v", err)
		w.setUpdateState(updateFailed, err.Error())
		w.mu.Lock()
		w.updating = false
		w.mu.Unlock()
		return
	}
	w.exit()
}

// installUpdate leaves the new version running and waiting for this copy to
// exit, or the old exe in place.
func (w *WebUI) installUpdate(rel update.Release, exe string) error {
	ctx, cancel := context.WithTimeout(context.Background(), updateInstallTimeout)
	defer cancel()
	staged, err := w.updater.Stage(ctx, rel, exe)
	if err != nil {
		return err
	}
	w.setUpdateState(updateInstalling, "")

	w.swapMu.Lock()
	defer w.swapMu.Unlock()
	if w.swapClosed {
		os.Remove(staged)
		return errors.New("Sparks is closing")
	}
	if err := update.Swap(exe, staged); err != nil {
		os.Remove(staged)
		return err
	}
	w.setUpdateState(updateRestarting, "")
	if err := w.restart(exe); err != nil {
		if backErr := update.RollBack(exe); backErr != nil {
			return fmt.Errorf("can't start the new version (%w), and your previous version is left as %s: %v",
				err, update.OldPath(exe), backErr)
		}
		return fmt.Errorf("can't start the new version: %w", err)
	}
	return nil
}

// closeUpdates waits out a swap in progress and stops any later one, so
// closing the window can't exit between the swap's two renames.
func (w *WebUI) closeUpdates() {
	w.swapMu.Lock()
	defer w.swapMu.Unlock()
	w.swapClosed = true
}

// runningExe returns the path of the running program.
func runningExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}
