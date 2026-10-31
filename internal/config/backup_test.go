package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func saveSpeed(t *testing.T, path string, wpm int) {
	t.Helper()
	if err := SaveAppConfig(path, &AppConfig{KeyerSpeed: wpm}); err != nil {
		t.Fatalf("SaveAppConfig: %v", err)
	}
}

func listBackups(t *testing.T, path string) []string {
	t.Helper()
	names, err := ListBackups(path)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	return names
}

func TestSaveBacksUpPreviousConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	saveSpeed(t, path, 20)
	if got := listBackups(t, path); len(got) != 0 {
		t.Fatalf("first save made backups %v, want none", got)
	}
	saveSpeed(t, path, 30)
	got := listBackups(t, path)
	if len(got) != 1 {
		t.Fatalf("backups = %v, want one", got)
	}
	data, err := os.ReadFile(filepath.Join(BackupDir(path), got[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"keyer_speed": 20`) {
		t.Fatalf("backup holds %s, want the old config", data)
	}
}

func TestSaveUnchangedMakesNoBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	saveSpeed(t, path, 20)
	saveSpeed(t, path, 20)
	if got := listBackups(t, path); len(got) != 0 {
		t.Fatalf("backups = %v, want none: Start saves every time", got)
	}
}

func TestSaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sparks-config.json")
	saveSpeed(t, path, 20)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir holds %v, want only the config", entries)
	}
}

func TestSaveKeepsNewestBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	for i := range backupKeep + 5 {
		saveSpeed(t, path, i+1)
	}
	got := listBackups(t, path)
	if len(got) != backupKeep {
		t.Fatalf("kept %d backups, want %d", len(got), backupKeep)
	}
	newest, err := os.ReadFile(filepath.Join(BackupDir(path), got[0]))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(`"keyer_speed": %d`, backupKeep+4); !strings.Contains(string(newest), want) {
		t.Fatalf("newest backup holds %s, want %s", newest, want)
	}
}

// TestSaveRefusedWhenCurrentUnreadable: a config that can't be backed up must
// not be replaced.
func TestSaveRefusedWhenCurrentUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SaveAppConfig(path, &AppConfig{}); err == nil {
		t.Fatal("save over an unreadable config succeeded")
	}
}

func TestBackupsFollowTheConfigName(t *testing.T) {
	dir := t.TempDir()
	one, two := filepath.Join(dir, "radio1.json"), filepath.Join(dir, "radio2.json")
	saveSpeed(t, one, 20)
	saveSpeed(t, one, 30)
	saveSpeed(t, two, 20)
	saveSpeed(t, two, 30)
	saveSpeed(t, two, 40)
	if a, b := listBackups(t, one), listBackups(t, two); len(a) != 1 || len(b) != 2 {
		t.Fatalf("backups = %v and %v, want 1 and 2", a, b)
	}
}

// TestBackupsDontMixSimilarNames: radio.json's backups start with "radio-", as
// do radio-home.json's, but neither config may list, restore or prune the other's.
func TestBackupsDontMixSimilarNames(t *testing.T) {
	dir := t.TempDir()
	radio, home := filepath.Join(dir, "radio.json"), filepath.Join(dir, "radio-home.json")
	saveSpeed(t, radio, 20)
	saveSpeed(t, radio, 30)
	want := listBackups(t, radio)
	if len(want) != 1 {
		t.Fatalf("radio backups = %v, want one", want)
	}
	for i := range backupKeep + 5 {
		saveSpeed(t, home, i+1)
	}

	if got := listBackups(t, radio); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("radio backups = %v, want only %v", got, want)
	}
	homeBackups := listBackups(t, home)
	if len(homeBackups) != backupKeep {
		t.Fatalf("home kept %d backups, want %d", len(homeBackups), backupKeep)
	}
	if _, err := RestoreBackup(radio, homeBackups[0]); err == nil {
		t.Fatal("radio restored a backup of radio-home")
	}
	if _, err := RestoreBackup(radio, want[0]); err != nil {
		t.Fatalf("radio's own backup: %v", err)
	}
}

func TestListBackupsNoneYet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	if got := listBackups(t, path); len(got) != 0 {
		t.Fatalf("backups = %v, want none", got)
	}
}

func TestRestoreBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	saveSpeed(t, path, 20)
	saveSpeed(t, path, 30)
	cfg, err := RestoreBackup(path, listBackups(t, path)[0])
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if cfg.KeyerSpeed != 20 {
		t.Fatalf("restored speed = %d, want 20", cfg.KeyerSpeed)
	}
	loaded, err := LoadAppConfig(path)
	if err != nil || loaded.KeyerSpeed != 20 {
		t.Fatalf("config on disk = %+v, %v; want speed 20", loaded, err)
	}
	// The config it replaced is backed up, so a restore can be undone.
	if got := listBackups(t, path); len(got) != 2 {
		t.Fatalf("backups = %v, want the replaced config kept too", got)
	}
}

// TestRestoreOverCorruptConfigKeepsIt: a damaged config may still be
// recoverable by hand.
func TestRestoreOverCorruptConfigKeepsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	saveSpeed(t, path, 20)
	saveSpeed(t, path, 30)
	good := listBackups(t, path)[0]
	if err := os.WriteFile(path, []byte(`{"keyer_spe`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreBackup(path, good); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(BackupDir(path), listBackups(t, path)[0]))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"keyer_spe` {
		t.Fatalf("newest backup = %q, want the damaged file", data)
	}
}

func TestRestoreRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sparks-config.json")
	saveSpeed(t, path, 20)
	saveSpeed(t, path, 30)
	outside := filepath.Join(dir, "secret.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "../secret.json", "..", "sparks-config.json", `..\secret.json`, "other-1.json"} {
		if _, err := RestoreBackup(path, name); err == nil {
			t.Errorf("RestoreBackup(%q) succeeded", name)
		}
	}
}

func TestRestoreRejectsCorruptBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	saveSpeed(t, path, 20)
	saveSpeed(t, path, 30)
	name := listBackups(t, path)[0]
	if err := os.WriteFile(filepath.Join(BackupDir(path), name), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreBackup(path, name); err == nil {
		t.Fatal("restoring a corrupt backup succeeded")
	}
	if loaded, err := LoadAppConfig(path); err != nil || loaded.KeyerSpeed != 30 {
		t.Fatalf("config on disk = %+v, %v; want speed 30 untouched", loaded, err)
	}
}
