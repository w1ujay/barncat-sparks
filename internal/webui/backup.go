package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ujay/barncat-sparks/internal/config"
)

// localOnly refuses requests that didn't come from Sparks's own page. A website
// open in the same browser can reach this port: the Origin check stops it
// posting here, and the Host check stops DNS rebinding reading the config.
func localOnly(addr string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if r.Host != addr || (origin != "" && origin != "http://"+addr) {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

// configErrorMsg explains why saving is off. Call with w.mu held.
func (w *WebUI) configErrorMsg() string {
	if w.cfgLoadErr == "" {
		return ""
	}
	return fmt.Sprintf("Your settings file could not be read, so saving is off to protect it. "+
		"Close and reopen Sparks, or restore a backup. (%s)", w.cfgLoadErr)
}

// handleConfigBackups lists the backups, newest first, and any config error.
func (w *WebUI) handleConfigBackups(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	names, err := config.ListBackups(w.cfgPath)
	if err != nil {
		writeJSONCode(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.mu.Lock()
	msg := w.configErrorMsg()
	w.mu.Unlock()
	writeJSON(rw, map[string]any{"backups": names, "config_error": msg})
}

// handleConfigRestore replaces the config with a backup. It is refused while
// running, since the subsystems were started from the current config.
func (w *WebUI) handleConfigRestore(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireJSONContentType(rw, r) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		writeJSONCode(rw, http.StatusConflict, map[string]string{"error": "Stop Sparks before restoring a backup"})
		return
	}
	cfg, err := config.RestoreBackup(w.cfgPath, req.Name)
	if err != nil {
		code := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "invalid backup name") {
			code = http.StatusBadRequest
		}
		writeJSONCode(rw, code, map[string]string{"error": err.Error()})
		return
	}
	w.config = cfg
	w.cfgLoadErr = ""
	writeJSON(rw, map[string]string{"status": "restored"})
}
