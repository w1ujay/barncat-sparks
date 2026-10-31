package webui

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// fakeGitHub serves tag as the latest release.
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

// installFixture is a WebUI with a newer release waiting on a fake GitHub, and
// an exe in a temp folder standing in for the running program.
type installFixture struct {
	w         *WebUI
	exe       string
	restarted []string
	exited    chan struct{}
}

func newInstallFixture(t *testing.T, checksumOK bool) *installFixture {
	t.Helper()
	setVersion(t, "0.4.0")

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	f, err := zw.Create("barncat-sparks.exe")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("MZ new"))
	zw.Close()
	zipBytes := zipBuf.Bytes()

	const zipName = "barncat-sparks_0.5.0_windows_amd64.zip"
	hash := sha256.Sum256(zipBytes)
	if !checksumOK {
		hash[0] ^= 0xff
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			fmt.Fprintf(rw, `{"tag_name": "v0.5.0", "html_url": "https://github.com/%s/releases/tag/v0.5.0",
				"assets": [{"name": %q, "browser_download_url": "%s/zip"},
				           {"name": "checksums.txt", "browser_download_url": "%s/sums"}]}`,
				update.Repo, zipName, srv.URL, srv.URL)
		case r.URL.Path == "/zip":
			rw.Write(zipBytes)
		case r.URL.Path == "/sums":
			fmt.Fprintf(rw, "%x  %s\n", hash, zipName)
		}
	}))
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "barncat-sparks.exe")
	if err := os.WriteFile(exe, []byte("MZ old"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx := &installFixture{exe: exe, exited: make(chan struct{})}
	fx.w = &WebUI{
		updater:    &update.Client{HTTP: srv.Client(), APIBase: srv.URL},
		selfUpdate: true,
		exePath:    func() (string, error) { return exe, nil },
		restart: func(path string) error {
			fx.restarted = append(fx.restarted, path)
			return nil
		},
		exit: func() { close(fx.exited) },
	}
	fx.w.checkForUpdate(context.Background())
	return fx
}

func (fx *installFixture) post(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/update/install", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fx.w.handleUpdateInstall(rec, req)
	return rec
}

// waitForState polls until the update reaches state.
func (fx *installFixture) waitForState(t *testing.T, state string) updateStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := getUpdateStatus(t, fx.w); s.State == state {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never reached %q: %+v", state, getUpdateStatus(t, fx.w))
	return updateStatus{}
}

func assertExe(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", filepath.Base(path), got, want)
	}
}

func TestUpdateInstallSwapsRestartsAndExits(t *testing.T) {
	fx := newInstallFixture(t, true)
	if !getUpdateStatus(t, fx.w).CanInstall {
		t.Fatal("can_install = false")
	}
	if rec := fx.post(t); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body)
	}
	select {
	case <-fx.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("never exited")
	}
	fx.waitForState(t, updateRestarting)
	assertExe(t, fx.exe, "MZ new")
	assertExe(t, fx.exe+".old", "MZ old")
	if len(fx.restarted) != 1 || fx.restarted[0] != fx.exe {
		t.Fatalf("restarted = %v", fx.restarted)
	}
}

func TestUpdateInstallRefusedWhileRunning(t *testing.T) {
	fx := newInstallFixture(t, true)
	fx.w.running = true
	rec := fx.post(t)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Stop Sparks") {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body)
	}
	if s := getUpdateStatus(t, fx.w); s.State != "" {
		t.Fatalf("state = %q", s.State)
	}
}

func TestUpdateInstallRefusedOffWindows(t *testing.T) {
	fx := newInstallFixture(t, true)
	fx.w.selfUpdate = false
	if getUpdateStatus(t, fx.w).CanInstall {
		t.Fatal("can_install = true")
	}
	if rec := fx.post(t); rec.Code != http.StatusConflict {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestUpdateInstallRefusedWithNoRelease(t *testing.T) {
	w := &WebUI{selfUpdate: true}
	req := httptest.NewRequest(http.MethodPost, "/api/update/install", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	w.handleUpdateInstall(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestUpdateInstallMethodAndContentType(t *testing.T) {
	fx := newInstallFixture(t, true)
	rec := httptest.NewRecorder()
	fx.w.handleUpdateInstall(rec, httptest.NewRequest(http.MethodGet, "/api/update/install", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET code = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	fx.w.handleUpdateInstall(rec, httptest.NewRequest(http.MethodPost, "/api/update/install", nil))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("no content type: code = %d", rec.Code)
	}
}

func TestUpdateInstallOneAtATime(t *testing.T) {
	fx := newInstallFixture(t, true)
	fx.w.updating = true
	if rec := fx.post(t); rec.Code != http.StatusConflict {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestUpdateFailureLeavesExeAndAllowsRetry(t *testing.T) {
	fx := newInstallFixture(t, false)
	if rec := fx.post(t); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d", rec.Code)
	}
	s := fx.waitForState(t, updateFailed)
	if !strings.Contains(s.Error, "checksum") {
		t.Fatalf("error = %q", s.Error)
	}
	assertExe(t, fx.exe, "MZ old")
	if len(fx.restarted) != 0 {
		t.Fatal("restarted after a failed update")
	}
	if rec := fx.post(t); rec.Code != http.StatusAccepted {
		t.Fatalf("retry code = %d: %s", rec.Code, rec.Body)
	}
	fx.waitForState(t, updateFailed)
}

func TestUpdateRollsBackWhenRestartFails(t *testing.T) {
	fx := newInstallFixture(t, true)
	fx.w.restart = func(string) error { return errors.New("blocked by antivirus") }
	if rec := fx.post(t); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d", rec.Code)
	}
	s := fx.waitForState(t, updateFailed)
	if !strings.Contains(s.Error, "blocked by antivirus") {
		t.Fatalf("error = %q", s.Error)
	}
	assertExe(t, fx.exe, "MZ old")
	if _, err := os.Stat(fx.exe + ".old"); !os.IsNotExist(err) {
		t.Fatalf("rollback file left behind: %v", err)
	}
	select {
	case <-fx.exited:
		t.Fatal("exited with no new copy started")
	default:
	}
}

func TestStartRefusedDuringUpdate(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "sparks-config.json")
	w := &WebUI{updating: true, config: config.DefaultAppConfig(), cfgPath: cfgPath}
	rec := httptest.NewRecorder()
	w.handleStart(rec, httptest.NewRequest(http.MethodPost, "/api/start", nil))
	var s statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Running || !strings.Contains(s.Message, "updat") {
		t.Fatalf("status = %+v", s)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatal("Start went on to save the config")
	}
}

func TestIndexWiresUpdateInstall(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="updateBtn"`, `id="updateNote"`, `'/api/update/install'`, `function installUpdate(`,
		`function watchUpdate(`, `s.can_install`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
}

// offerBoth serves a stable release and a newer pre-release.
func offerBoth(t *testing.T) *update.Client {
	t.Helper()
	release := func(tag string, pre bool) string {
		return fmt.Sprintf(`{"tag_name": %q, "html_url": "https://github.com/%s/releases/tag/%s", "prerelease": %v}`,
			tag, update.Repo, tag, pre)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			fmt.Fprint(rw, release("v0.4.0", false))
			return
		}
		fmt.Fprintf(rw, "[%s,%s]", release("v0.5.0-rc.1", true), release("v0.4.0", false))
	}))
	t.Cleanup(srv.Close)
	return &update.Client{HTTP: srv.Client(), APIBase: srv.URL}
}

func TestUpdateCheckSkipsPrereleasesByDefault(t *testing.T) {
	setVersion(t, "0.3.2")
	w := &WebUI{updater: offerBoth(t), config: config.DefaultAppConfig()}
	w.checkForUpdate(context.Background())
	if s := getUpdateStatus(t, w); s.Latest != "0.4.0" {
		t.Fatalf("status = %+v, want the stable release", s)
	}
}

func TestUpdateCheckOffersPrereleasesWhenTurnedOn(t *testing.T) {
	setVersion(t, "0.3.2")
	cfg := config.DefaultAppConfig()
	cfg.UpdatePrereleases = true
	w := &WebUI{updater: offerBoth(t), config: cfg}
	w.checkForUpdate(context.Background())
	if s := getUpdateStatus(t, w); s.Latest != "0.5.0-rc.1" {
		t.Fatalf("status = %+v, want the pre-release", s)
	}
}

func TestIndexWiresPrereleaseSetting(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="updatePrereleases"`, `update_prereleases:`, `cfg.update_prereleases`,
		`onchange="onPrereleaseChange(this)"`, `function onPrereleaseChange(`, `next time Sparks starts`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
}
