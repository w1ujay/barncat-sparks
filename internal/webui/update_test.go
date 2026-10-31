package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ujay/barncat-sparks/internal/config"
	"github.com/ujay/barncat-sparks/internal/update"
)

// setVersion sets the running version for one test.
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := config.Version
	config.Version = v
	t.Cleanup(func() { config.Version = old })
}

// fakeGitHub serves tag as the latest release, with the given extra assets.
func fakeGitHub(t *testing.T, tag string) *update.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"tag_name": %q, "html_url": "https://github.com/%s/releases/tag/%s"}`, tag, update.Repo, tag)
	}))
	t.Cleanup(srv.Close)
	return &update.Client{HTTP: srv.Client(), APIBase: srv.URL}
}

func deadGitHub(t *testing.T) *update.Client {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return &update.Client{HTTP: http.DefaultClient, APIBase: base}
}

func getUpdateStatus(t *testing.T, w *WebUI) updateStatus {
	t.Helper()
	rec := httptest.NewRecorder()
	w.handleUpdateStatus(rec, httptest.NewRequest(http.MethodGet, "/api/update", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var s updateStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateStatusBeforeCheck(t *testing.T) {
	s := getUpdateStatus(t, &WebUI{})
	if s.Checked || s.Available {
		t.Fatalf("status = %+v, want not checked yet", s)
	}
}

func TestUpdateCheckFindsNewerRelease(t *testing.T) {
	setVersion(t, "0.4.0")
	w := &WebUI{updater: fakeGitHub(t, "v0.5.0")}
	w.checkForUpdate(context.Background())
	s := getUpdateStatus(t, w)
	if !s.Checked || !s.Available || s.Latest != "0.5.0" {
		t.Fatalf("status = %+v", s)
	}
	if !strings.HasSuffix(s.URL, "/releases/tag/v0.5.0") {
		t.Fatalf("url = %q", s.URL)
	}
}

func TestUpdateCheckStaysQuietWhenCurrent(t *testing.T) {
	setVersion(t, "0.5.0")
	w := &WebUI{updater: fakeGitHub(t, "v0.5.0")}
	w.checkForUpdate(context.Background())
	if s := getUpdateStatus(t, w); !s.Checked || s.Available || s.URL != "" {
		t.Fatalf("status = %+v", s)
	}
}

func TestUpdateCheckStaysQuietWithNoInternet(t *testing.T) {
	setVersion(t, "0.4.0")
	w := &WebUI{updater: deadGitHub(t)}
	w.checkForUpdate(context.Background())
	if s := getUpdateStatus(t, w); !s.Checked || s.Available {
		t.Fatalf("status = %+v, want checked and nothing to show", s)
	}
}

func TestUpdateOpenOpensReleasePage(t *testing.T) {
	setVersion(t, "0.4.0")
	var opened string
	w := &WebUI{updater: fakeGitHub(t, "v0.5.0"), openURL: func(u string) { opened = u }}
	w.checkForUpdate(context.Background())

	req := httptest.NewRequest(http.MethodPost, "/api/update/open", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	w.handleUpdateOpen(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.HasSuffix(opened, "/releases/tag/v0.5.0") {
		t.Fatalf("opened %q", opened)
	}
}

func TestUpdateOpenRefusals(t *testing.T) {
	var opened bool
	w := &WebUI{openURL: func(string) { opened = true }}

	rec := httptest.NewRecorder()
	w.handleUpdateOpen(rec, httptest.NewRequest(http.MethodGet, "/api/update/open", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET code = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	w.handleUpdateOpen(rec, httptest.NewRequest(http.MethodPost, "/api/update/open", nil))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("no content type: code = %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/update/open", nil)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	w.handleUpdateOpen(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("nothing to open: code = %d", rec.Code)
	}
	if opened {
		t.Error("opened a page on a refused request")
	}
}

func TestIndexWiresUpdate(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="updateBar"`, `id="updateLink"`, `'/api/update'`, `'/api/update/open'`,
		`function pollUpdate(`, `Update available`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
}
