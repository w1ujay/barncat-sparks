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
// POST requests get a Content-Type: application/json header, as the real
// page's fetch() calls always send.
func callJSON(t *testing.T, handler http.HandlerFunc, method, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	handler(rec, req)
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

func TestHearthWolNotConfigured(t *testing.T) {
	w := hearthWebUI(&config.AppConfig{})
	_, out := callJSON(t, w.handleHearthWol, http.MethodGet, "")
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "hearth_url") {
		t.Errorf("response = %v, want the not-configured error", out)
	}
	if _, has := out["targets"]; has {
		t.Errorf("response = %v, want no targets on error", out)
	}
}

func TestHearthWolListsTargets(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"GET /api/wol": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"targets":[{"name":"lempster-laptop"}]}`)
		},
		"GET /api/wol/lempster-laptop/status": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"online":true,"source":"tailscale"}`)
		},
	})
	w := hearthWebUI(&config.AppConfig{HearthURL: pi.URL})
	_, out := callJSON(t, w.handleHearthWol, http.MethodGet, "")
	targets, _ := out["targets"].([]any)
	if len(targets) != 1 {
		t.Fatalf("response = %v, want one target", out)
	}
	tg, _ := targets[0].(map[string]any)
	if tg["name"] != "lempster-laptop" || tg["online"] != true || tg["source"] != "tailscale" {
		t.Errorf("target = %v", tg)
	}
}

// No targets is an empty array, never null, so the page can always iterate.
func TestHearthWolEmptyTargetsIsArray(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"GET /api/wol": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"targets":[]}`)
		},
	})
	w := hearthWebUI(&config.AppConfig{HearthURL: pi.URL})
	_, out := callJSON(t, w.handleHearthWol, http.MethodGet, "")
	if targets, ok := out["targets"].([]any); !ok || len(targets) != 0 {
		t.Errorf("response = %v, want targets: []", out)
	}
}

func TestHearthWolWakePassesForbiddenThrough(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"POST /api/wol/lempster-laptop": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `operator sparks-test lacks scope "wol"`, http.StatusForbidden)
		},
	})
	w := hearthWebUI(&config.AppConfig{HearthURL: pi.URL})
	_, out := callJSON(t, w.handleHearthWol, http.MethodPost, `{"name":"lempster-laptop"}`)
	if out["status"] != float64(403) || out["ok"] == true ||
		out["error"] != `not permitted: operator sparks-test lacks scope "wol"` {
		t.Errorf("response = %v", out)
	}
}

func TestHearthWolWakeOK(t *testing.T) {
	pi := fakeHearthServer(t, map[string]http.HandlerFunc{
		"POST /api/wol/lempster-laptop": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"sent":true,"name":"lempster-laptop"}`)
		},
	})
	w := hearthWebUI(&config.AppConfig{HearthURL: pi.URL})
	_, out := callJSON(t, w.handleHearthWol, http.MethodPost, `{"name":"lempster-laptop"}`)
	if out["ok"] != true {
		t.Errorf("response = %v, want ok", out)
	}
}

func TestHearthWolWakeRequiresName(t *testing.T) {
	w := hearthWebUI(&config.AppConfig{HearthURL: "http://127.0.0.1:1"})
	code, _ := callJSON(t, w.handleHearthWol, http.MethodPost, `{}`)
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestHearthWolRejectsOtherMethods(t *testing.T) {
	w := hearthWebUI(&config.AppConfig{HearthURL: "http://127.0.0.1:1"})
	code, _ := callJSON(t, w.handleHearthWol, http.MethodPut, "")
	if code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", code)
	}
}

// TestHearthPostRoutesRejectNonJSONContentType guards against a cross-site
// "simple" request: any page in the operator's browser can POST
// text/plain (no CORS preflight) and have it land as if it were JSON. The
// hearth POST handlers must reject a non-application/json body before
// decoding it or contacting hearth at all.
func TestHearthPostRoutesRejectNonJSONContentType(t *testing.T) {
	dead := "http://" + freeAddr(t)
	w := hearthWebUI(&config.AppConfig{HearthURL: dead})
	cases := []struct {
		name    string
		handler http.HandlerFunc
		body    string
	}{
		{"test", w.handleHearthTest, `{"url":"` + dead + `"}`},
		{"k4", w.handleHearthK4, `{"power":"on"}`},
		{"powersave", w.handleHearthPowerSave, `{"armed":true}`},
		{"wol", w.handleHearthWol, `{"name":"lempster-laptop"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "text/plain")
			c.handler(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("status = %d, want 415", rec.Code)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != "Content-Type must be application/json" {
				t.Errorf("body = %q, want %q", got, "Content-Type must be application/json")
			}
		})
	}
}

// TestIndexWiresStation guards the page's hooks into the hearth routes and
// config keys; the JS itself is verified manually (Step 6).
func TestIndexWiresStation(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="stationPanel"`, `id="hearthToken"`, `id="k4OnBtn"`, `id="psBtn"`,
		`'/api/hearth/status'`, `'/api/hearth/test'`, `'/api/hearth/k4'`, `'/api/hearth/powersave'`,
		`hearth_url:`, `hearth_replica_url:`, `hearth_token:`,
		`id="operateView"`, `id="settingsView"`, `id="stationHint"`, `id="saveMsg"`, `showTab(`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
	if strings.Contains(body, "innerHTML = hearth") {
		t.Error("hearth-derived text must be rendered with textContent")
	}
}

// TestIndexSplitsOperateAndSettings pins which view each control lives in:
// day-to-day controls on Operate, every config field and Save on Settings.
func TestIndexSplitsOperateAndSettings(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()

	operate := strings.Index(body, `id="operateView"`)
	settings := strings.Index(body, `id="settingsView"`)
	if operate < 0 || settings < 0 || operate > settings {
		t.Fatalf("operateView at %d, settingsView at %d; want operateView first", operate, settings)
	}
	for _, id := range []string{`id="startBtn"`, `id="stopBtn"`, `id="statusBar"`, `id="stationPanel"`, `id="k4OnBtn"`, `id="psBtn"`, `id="stationMsg"`} {
		if i := strings.Index(body, id); i < operate || i > settings {
			t.Errorf("%s is not inside operateView", id)
		}
	}
	for _, id := range []string{`id="catAddr"`, `id="serialPort"`, `id="hearthUrl"`, `id="hearthToken"`, `id="hearthIdentity"`, `id="midiDevicesContainer"`, `id="paddleSwap"`, `id="saveMsg"`, `id="aboutSection"`} {
		if i := strings.Index(body, id); i < settings {
			t.Errorf("%s is not inside settingsView", id)
		}
	}
	if strings.Contains(body, `id="settingsView">`) {
		t.Error("settingsView must start hidden so the page opens on Operate")
	}
}
