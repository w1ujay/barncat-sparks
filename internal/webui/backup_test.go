package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ujay/barncat-sparks/internal/config"
)

func TestLocalOnly(t *testing.T) {
	const addr = "127.0.0.1:5000"
	h := localOnly(addr, http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name, method, host, origin string
		want                       int
	}{
		{"page GET", http.MethodGet, addr, "", http.StatusNoContent},
		{"page POST", http.MethodPost, addr, "http://" + addr, http.StatusNoContent},
		{"website POST", http.MethodPost, addr, "https://evil.example", http.StatusForbidden},
		{"other port POST", http.MethodPost, addr, "http://127.0.0.1:6000", http.StatusForbidden},
		{"rebound host GET", http.MethodGet, "evil.example:5000", "", http.StatusForbidden},
		{"rebound host POST", http.MethodPost, "evil.example:5000", "http://evil.example:5000", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/config", nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("code = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

// TestLocalOnlyOverRealServer checks the address the server really reports:
// if it ever differs from the page's URL, the guard would lock the page out.
func TestLocalOnlyOverRealServer(t *testing.T) {
	var ok http.HandlerFunc = func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	}
	srv := httptest.NewUnstartedServer(nil)
	addr := srv.Listener.Addr().String()
	srv.Config.Handler = localOnly(addr, ok)
	srv.Start()
	defer srv.Close()

	url := "http://" + addr + "/api/config"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("page GET = %d, want 204", resp.StatusCode)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://"+addr)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("page POST = %d, want 204", resp.StatusCode)
	}
}

// damagedUI is a WebUI that found its config unreadable at startup.
func damagedUI(t *testing.T) (*WebUI, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	if err := os.WriteFile(path, []byte(`{"keyer_spe`), 0644); err != nil {
		t.Fatal(err)
	}
	return &WebUI{config: config.DefaultAppConfig(), cfgPath: path, cfgLoadErr: "parse config: bad"}, path
}

func TestSaveRefusedWhileConfigUnreadable(t *testing.T) {
	w, path := damagedUI(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"keyer_speed":25}`))
	w.handleConfig(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("save = %d, want 409", rec.Code)
	}
	if data, _ := os.ReadFile(path); string(data) != `{"keyer_spe` {
		t.Fatalf("config on disk = %q, want it untouched", data)
	}
}

// TestStartRefusedWhileConfigUnreadable: Start saves too, so it must not write
// the blank defaults over the file.
func TestStartRefusedWhileConfigUnreadable(t *testing.T) {
	w, path := damagedUI(t)
	code, out := callJSON(t, w.handleStart, http.MethodPost, "")
	if code != http.StatusOK || out["running"] != false || !strings.Contains(out["message"].(string), "restore") {
		t.Fatalf("start = %d %v, want not running and a restore message", code, out)
	}
	if data, _ := os.ReadFile(path); string(data) != `{"keyer_spe` {
		t.Fatalf("config on disk = %q, want it untouched", data)
	}
}

func TestBackupsListReportsConfigError(t *testing.T) {
	w, _ := damagedUI(t)
	code, out := callJSON(t, w.handleConfigBackups, http.MethodGet, "")
	if code != http.StatusOK || !strings.Contains(out["config_error"].(string), "restore") {
		t.Fatalf("backups = %d %v, want the config error", code, out)
	}
	if got, ok := out["backups"].([]any); !ok || len(got) != 0 {
		t.Fatalf("backups = %v, want an empty list", out["backups"])
	}
}

func backedUpUI(t *testing.T) (*WebUI, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	for _, wpm := range []int{20, 30} {
		if err := config.SaveAppConfig(path, &config.AppConfig{KeyerSpeed: wpm}); err != nil {
			t.Fatal(err)
		}
	}
	names, err := config.ListBackups(path)
	if err != nil || len(names) != 1 {
		t.Fatalf("backups = %v, %v", names, err)
	}
	return &WebUI{config: &config.AppConfig{KeyerSpeed: 30}, cfgPath: path}, path, names[0]
}

func TestRestoreClearsConfigError(t *testing.T) {
	w, _, name := backedUpUI(t)
	w.cfgLoadErr = "parse config: bad"
	code, out := callJSON(t, w.handleConfigRestore, http.MethodPost, `{"name":"`+name+`"}`)
	if code != http.StatusOK {
		t.Fatalf("restore = %d %v, want 200", code, out)
	}
	if w.cfgLoadErr != "" || w.config.KeyerSpeed != 20 {
		t.Fatalf("after restore: err %q speed %d, want cleared and 20", w.cfgLoadErr, w.config.KeyerSpeed)
	}
}

func TestRestoreRefusedWhileRunning(t *testing.T) {
	w, _, name := backedUpUI(t)
	w.running = true
	code, _ := callJSON(t, w.handleConfigRestore, http.MethodPost, `{"name":"`+name+`"}`)
	if code != http.StatusConflict {
		t.Fatalf("restore while running = %d, want 409", code)
	}
	if w.config.KeyerSpeed != 30 {
		t.Fatalf("speed = %d, want the running config untouched", w.config.KeyerSpeed)
	}
}

func TestRestoreBadName(t *testing.T) {
	w, _, _ := backedUpUI(t)
	if code, _ := callJSON(t, w.handleConfigRestore, http.MethodPost, `{"name":"../x.json"}`); code != http.StatusBadRequest {
		t.Fatalf("restore bad name = %d, want 400", code)
	}
}

func TestRestoreMethodAndContentType(t *testing.T) {
	w, _, _ := backedUpUI(t)
	if code, _ := callJSON(t, w.handleConfigRestore, http.MethodGet, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", code)
	}
	rec := httptest.NewRecorder()
	w.handleConfigRestore(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST without JSON content type = %d, want 415", rec.Code)
	}
}

// TestIndexWiresBackups guards the restore controls on the Settings page.
func TestIndexWiresBackups(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="backupSelect"`, `'/api/config/backups'`, `'/api/config/restore'`, `function restoreBackup(`, `id="configError"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
}
