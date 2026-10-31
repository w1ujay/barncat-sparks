package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testVersion = "0.5.0"

var (
	testZipName = "barncat-sparks_" + testVersion + "_windows_amd64.zip"
	newExe      = []byte("MZ new sparks")
)

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// releaseServer serves name → body as release assets. A nil body is a 500.
func releaseServer(t *testing.T, assets map[string][]byte) (*Client, Release) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, ok := assets[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok || body == nil {
			http.Error(rw, "nope", http.StatusInternalServerError)
			return
		}
		rw.Write(body)
	}))
	t.Cleanup(srv.Close)
	rel := Release{Version: testVersion}
	for name := range assets {
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: srv.URL + "/" + name})
	}
	return &Client{HTTP: srv.Client()}, rel
}

func goodAssets(t *testing.T) map[string][]byte {
	t.Helper()
	z := makeZip(t, map[string][]byte{"barncat-sparks.exe": newExe, "README.md": []byte("hi")})
	return map[string][]byte{
		testZipName:     z,
		"checksums.txt": fmt.Appendf(nil, "%s  other.tar.gz\n%s  %s\n", strings.Repeat("0", 64), sum(z), testZipName),
	}
}

// installDir returns a folder holding an old exe, as a user's install would.
func installDir(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "barncat-sparks.exe")
	if err := os.WriteFile(exe, []byte("MZ old sparks"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// assertOnlyFiles fails if the folder holds anything but the named files.
func assertOnlyFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("folder holds %v, want %v", got, want)
	}
}

func TestStage(t *testing.T) {
	c, rel := releaseServer(t, goodAssets(t))
	exe := installDir(t)
	staged, err := c.Stage(context.Background(), rel, exe)
	if err != nil {
		t.Fatal(err)
	}
	if staged != exe+".new" {
		t.Fatalf("staged at %q", staged)
	}
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newExe) {
		t.Fatalf("staged content = %q", got)
	}
	assertOnlyFiles(t, filepath.Dir(exe), "barncat-sparks.exe", "barncat-sparks.exe.new")
	old, _ := os.ReadFile(exe)
	if string(old) != "MZ old sparks" {
		t.Fatal("Stage touched the running exe")
	}
}

func TestStageRefusals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, a map[string][]byte)
		want   string
	}{
		{"checksum mismatch", func(t *testing.T, a map[string][]byte) {
			a[testZipName] = append(a[testZipName], 0)
		}, "checksum"},
		{"zip not in checksums", func(t *testing.T, a map[string][]byte) {
			a["checksums.txt"] = []byte(strings.Repeat("0", 64) + "  other.zip\n")
		}, "checksum"},
		{"no zip in release", func(t *testing.T, a map[string][]byte) {
			delete(a, testZipName)
		}, "no Windows download"},
		{"no checksums in release", func(t *testing.T, a map[string][]byte) {
			delete(a, "checksums.txt")
		}, "checksums.txt"},
		{"download fails", func(t *testing.T, a map[string][]byte) {
			a[testZipName] = nil
		}, "500"},
		{"checksums download fails", func(t *testing.T, a map[string][]byte) {
			a["checksums.txt"] = nil
		}, "500"},
		{"no exe in zip", func(t *testing.T, a map[string][]byte) {
			z := makeZip(t, map[string][]byte{"README.md": []byte("hi")})
			a[testZipName] = z
			a["checksums.txt"] = fmt.Appendf(nil, "%s  %s\n", sum(z), testZipName)
		}, "barncat-sparks.exe"},
		{"exe is not a program", func(t *testing.T, a map[string][]byte) {
			z := makeZip(t, map[string][]byte{"barncat-sparks.exe": []byte("<html>")})
			a[testZipName] = z
			a["checksums.txt"] = fmt.Appendf(nil, "%s  %s\n", sum(z), testZipName)
		}, "not a Windows program"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assets := goodAssets(t)
			tt.mutate(t, assets)
			c, rel := releaseServer(t, assets)
			exe := installDir(t)
			_, err := c.Stage(context.Background(), rel, exe)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tt.want)
			}
			assertOnlyFiles(t, filepath.Dir(exe), "barncat-sparks.exe")
		})
	}
}

func TestParseChecksums(t *testing.T) {
	text := "ABC123  plain.zip\ndef456 *binary-mode.zip\n\nnot a line\n"
	if got, ok := findChecksum(text, "plain.zip"); !ok || got != "abc123" {
		t.Errorf("plain: %q %v", got, ok)
	}
	if got, ok := findChecksum(text, "binary-mode.zip"); !ok || got != "def456" {
		t.Errorf("binary mode: %q %v", got, ok)
	}
	if _, ok := findChecksum(text, "missing.zip"); ok {
		t.Error("found a file that isn't listed")
	}
}

func stageFile(t *testing.T, exe string, content string) string {
	t.Helper()
	staged := exe + ".new"
	if err := os.WriteFile(staged, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return staged
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", filepath.Base(path), got, want)
	}
}

func TestSwapKeepsOldExe(t *testing.T) {
	exe := installDir(t)
	if err := Swap(exe, stageFile(t, exe, "MZ new")); err != nil {
		t.Fatal(err)
	}
	assertFile(t, exe, "MZ new")
	assertFile(t, exe+".old", "MZ old sparks")
	assertOnlyFiles(t, filepath.Dir(exe), "barncat-sparks.exe", "barncat-sparks.exe.old")
}

func TestSwapReplacesEarlierRollback(t *testing.T) {
	exe := installDir(t)
	if err := os.WriteFile(exe+".old", []byte("MZ ancient"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Swap(exe, stageFile(t, exe, "MZ new")); err != nil {
		t.Fatal(err)
	}
	assertFile(t, exe+".old", "MZ old sparks")
}

// A copy still running from the rollback file keeps it from being removed.
func TestSwapLeavesExeWhenRollbackIsStuck(t *testing.T) {
	exe := installDir(t)
	if err := os.MkdirAll(filepath.Join(exe+".old", "stuck"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := stageFile(t, exe, "MZ new")
	err := Swap(exe, staged)
	if err == nil || !strings.Contains(err.Error(), "other copy") {
		t.Fatalf("err = %v", err)
	}
	assertFile(t, exe, "MZ old sparks")
}

func TestSwapRestoresExeWhenNewOneCannotMove(t *testing.T) {
	exe := installDir(t)
	if err := Swap(exe, exe+".missing"); err == nil {
		t.Fatal("want an error")
	}
	assertFile(t, exe, "MZ old sparks")
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatalf("old exe left behind: %v", err)
	}
}

func TestRollBack(t *testing.T) {
	exe := installDir(t)
	if err := Swap(exe, stageFile(t, exe, "MZ new")); err != nil {
		t.Fatal(err)
	}
	if err := RollBack(exe); err != nil {
		t.Fatal(err)
	}
	assertFile(t, exe, "MZ old sparks")
	assertOnlyFiles(t, filepath.Dir(exe), "barncat-sparks.exe")
}

func TestRestartArgs(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{nil, []string{"-wait-pid=42"}},
		{[]string{"-config", `C:\x y\a.json`}, []string{"-config", `C:\x y\a.json`, "-wait-pid=42"}},
		{[]string{"-wait-pid=7", "-config", "a"}, []string{"-config", "a", "-wait-pid=42"}},
		{[]string{"--wait-pid=7"}, []string{"-wait-pid=42"}},
	}
	for _, tt := range tests {
		if got := restartArgs(tt.args, 42); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("restartArgs(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestStartNewCopyFailsForMissingExe(t *testing.T) {
	if err := StartNewCopy(filepath.Join(t.TempDir(), "nope.exe"), nil, 42); err == nil {
		t.Fatal("want an error")
	}
}
