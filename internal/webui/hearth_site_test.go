package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ujay/barncat-sparks/internal/config"
)

// TestHearthStatusUsesConfiguredSite: the saved hearth_site reaches hearth as
// ?site=, which a multi-site hub replica requires.
func TestHearthStatusUsesConfiguredSite(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"GET /api/status": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("site") != "cabin" {
				http.Error(w, "site required: ?site=cabin|dragonroad", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"k4":{"card_state":"on","power_relay":"k4_power"}}`)
		},
	})
	w := hearthWebUI(&config.AppConfig{HearthURL: pi.URL, HearthSite: "cabin"})

	_, out := callJSON(t, w.handleHearthStatus, http.MethodGet, "")
	if out["source"] != "pi" {
		t.Errorf("response = %v, want source pi (the site must be sent)", out)
	}
}

// TestIndexWiresHearthSite guards the page's Site field and its config key.
func TestIndexWiresHearthSite(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{`id="hearthSite"`, `hearth_site:`, `cfg.hearth_site`} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
}
