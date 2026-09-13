package webui

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/ujay/barncat-sparks/internal/hearth"
)

// hearthResult is the JSON shape for hearth control responses. Status is
// hearth's HTTP status when hearth answered, so the page can tell a rejected
// token (401) from a missing scope (403) or a state conflict (409).
type hearthResult struct {
	OK     bool   `json:"ok,omitempty"`
	Queued bool   `json:"queued,omitempty"`
	Error  string `json:"error,omitempty"`
	Status int    `json:"status,omitempty"`
}

// requireJSONContentType rejects a POST whose media type is not
// application/json (charset and other parameters are fine), so a cross-site
// "simple" request (text/plain, no CORS preflight) can't reach hearth with a
// forwarded token. It writes the 415 response itself when rejecting.
func requireJSONContentType(rw http.ResponseWriter, r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		http.Error(rw, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

func hearthFailure(err error) hearthResult {
	res := hearthResult{Error: err.Error()}
	var he *hearth.HTTPError
	if errors.As(err, &he) {
		res.Status = he.Status
	}
	return res
}

// hearthClient builds a client from the saved config. The fields are copied
// under w.mu, which is released before any network I/O.
func (w *WebUI) hearthClient() *hearth.Client {
	w.mu.Lock()
	defer w.mu.Unlock()
	return &hearth.Client{
		PiURL:      w.config.HearthURL,
		ReplicaURL: w.config.HearthReplicaURL,
		Token:      w.config.HearthToken,
		Site:       w.config.HearthSite,
	}
}

// handleHearthStatus reports K4 and power-save state. Works while stopped.
func (w *WebUI) handleHearthStatus(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(rw, w.hearthClient().Status(r.Context()))
}

// handleHearthTest runs whoami against the posted (possibly unsaved) values.
func (w *WebUI) handleHearthTest(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireJSONContentType(rw, r) {
		return
	}
	var req struct {
		URL        string `json:"url"`
		ReplicaURL string `json:"replica_url"`
		Token      string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	c := &hearth.Client{PiURL: req.URL, ReplicaURL: req.ReplicaURL, Token: req.Token}
	id, err := c.WhoAmI(r.Context())
	if err != nil {
		writeJSON(rw, hearthFailure(err))
		return
	}
	writeJSON(rw, id)
}

// handleHearthK4 powers the cabin K4 on or off.
func (w *WebUI) handleHearthK4(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireJSONContentType(rw, r) {
		return
	}
	var req struct {
		Power string `json:"power"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Power != "on" && req.Power != "off" {
		http.Error(rw, `power must be "on" or "off"`, http.StatusBadRequest)
		return
	}
	if err := w.hearthClient().K4Power(r.Context(), req.Power == "on"); err != nil {
		writeJSON(rw, hearthFailure(err))
		return
	}
	writeJSON(rw, hearthResult{OK: true})
}

// handleHearthPowerSave arms or disarms power-save (queued on the replica
// when the Pi is unreachable).
func (w *WebUI) handleHearthPowerSave(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireJSONContentType(rw, r) {
		return
	}
	var req struct {
		Armed *bool `json:"armed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Armed == nil {
		http.Error(rw, `body must set "armed"`, http.StatusBadRequest)
		return
	}
	queued, err := w.hearthClient().SetPowerSave(r.Context(), *req.Armed)
	if err != nil {
		writeJSON(rw, hearthFailure(err))
		return
	}
	writeJSON(rw, hearthResult{OK: true, Queued: queued})
}
