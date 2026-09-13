package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ujay/barncat-sparks/internal/config"
)

// fakeHearthServer serves canned hearth responses keyed by "METHOD /path".
func fakeHearthServer(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.Method+" "+r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hearthWebUI(cfg *config.AppConfig) *WebUI {
	return &WebUI{config: cfg, statusMsg: "Stopped"}
}

// callJSON invokes handler with body and decodes the JSON response into a map.
func callJSON(t *testing.T, handler http.HandlerFunc, method, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(method, "/", strings.NewReader(body)))
	out := map[string]any{}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec.Code, out
}

func TestHearthStatusNotConfigured(t *testing.T) {
	w := hearthWebUI(&config.AppConfig{})
	_, out := callJSON(t, w.handleHearthStatus, http.MethodGet, "")
	if out["source"] != "unreachable" || !strings.Contains(out["error"].(string), "hearth_url") {
		t.Errorf("response = %v", out)
	}
}

func TestHearthStatusFromPi(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"GET /api/status": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"k4":{"card_state":"on","power_relay":"k4_power","freq_hz":7030000,"mode":"CW"}}`)
		},
	})
	w := hearthWebUI(&config.AppConfig{HearthURL: pi.URL})
	_, out := callJSON(t, w.handleHearthStatus, http.MethodGet, "")
	k4, _ := out["k4"].(map[string]any)
	if out["source"] != "pi" || k4["card_state"] != "on" || k4["freq_hz"] != float64(7030000) {
		t.Errorf("response = %v", out)
	}
}

func TestHearthTestUsesPostedValues(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"GET /api/whoami": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer formtoken" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"name":"K1ABC","owner":false,"scopes":["k4"],"replica":false}`)
		},
	})
	// Saved config points somewhere dead with a different token; the form wins.
	w := hearthWebUI(&config.AppConfig{HearthURL: "http://" + freeAddr(t), HearthToken: "saved"})
	body := `{"url":"` + pi.URL + `","replica_url":"","token":"formtoken"}`
	_, out := callJSON(t, w.handleHearthTest, http.MethodPost, body)
	if out["name"] != "K1ABC" {
		t.Errorf("response = %v, want identity K1ABC", out)
	}
}

func TestHearthTestReportsRejectedToken(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"GET /api/whoami": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		},
	})
	w := hearthWebUI(&config.AppConfig{})
	_, out := callJSON(t, w.handleHearthTest, http.MethodPost, `{"url":"`+pi.URL+`","token":"nope"}`)
	if out["error"] != "token rejected" || out["status"] != float64(401) {
		t.Errorf("response = %v, want token rejected / 401", out)
	}
}

func TestHearthK4PassesConflictThrough(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"POST /api/k4/power_on": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "the K4 is already on", http.StatusConflict)
		},
	})
	w := hearthWebUI(&config.AppConfig{HearthURL: pi.URL})
	_, out := callJSON(t, w.handleHearthK4, http.MethodPost, `{"power":"on"}`)
	if out["error"] != "the K4 is already on" || out["status"] != float64(409) || out["ok"] == true {
		t.Errorf("response = %v", out)
	}
}

func TestHearthK4RejectsBadPower(t *testing.T) {
	w := hearthWebUI(&config.AppConfig{HearthURL: "http://127.0.0.1:1"})
	code, _ := callJSON(t, w.handleHearthK4, http.MethodPost, `{"power":"maybe"}`)
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestHearthPowerSaveQueuedOnReplica(t *testing.T) {
	replica := fakeHearthServer(t, map[string]http.HandlerFunc{
		"POST /api/powersave": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"enabled":true,"replica":true,"desired":"off"}`)
		},
	})
	w := hearthWebUI(&config.AppConfig{HearthURL: "http://" + freeAddr(t), HearthReplicaURL: replica.URL})
	_, out := callJSON(t, w.handleHearthPowerSave, http.MethodPost, `{"armed":false}`)
	if out["ok"] != true || out["queued"] != true {
		t.Errorf("response = %v, want ok and queued", out)
	}
}

func TestHearthPowerSaveRequiresArmed(t *testing.T) {
	w := hearthWebUI(&config.AppConfig{HearthURL: "http://127.0.0.1:1"})
	code, _ := callJSON(t, w.handleHearthPowerSave, http.MethodPost, `{}`)
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}
