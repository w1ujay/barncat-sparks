package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// backupKeep is how many backups each config keeps. Start saves too, so a few
// edits can use up several.
const backupKeep = 20

// backupStamp is the timestamp in a backup's name. It sorts by time.
const backupStamp = "20060102-150405.000000000"

// saveMu makes each save, with its backup and prune, one step.
var saveMu sync.Mutex

// BackupDir returns the folder holding backups for the config at path.
func BackupDir(path string) string {
	return filepath.Join(filepath.Dir(path), "sparks-config-backups")
}

// backupPrefix starts the name of every backup of the config at path.
func backupPrefix(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base)) + "-"
}

// isBackupOf reports whether name is a backup of the config at path. Only a
// timestamp may follow the prefix, so radio.json doesn't claim the backups of
// radio-home.json in the folder they share.
func isBackupOf(path, name string) bool {
	rest, ok := strings.CutPrefix(name, backupPrefix(path))
	if !ok {
		return false
	}
	stamp, ok := strings.CutSuffix(rest, ".json")
	if !ok {
		return false
	}
	_, err := time.Parse(backupStamp, stamp)
	return err == nil
}

// ListBackups returns the backup file names for the config at path, newest
// first.
func ListBackups(path string) ([]string, error) {
	entries, err := os.ReadDir(BackupDir(path))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read backups: %w", err)
	}
	names := []string{}
	for _, e := range entries {
		if isBackupOf(path, e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// RestoreBackup replaces the config at path with the named backup and returns
// it. The config it replaces is backed up first, even a damaged one.
func RestoreBackup(path, name string) (*AppConfig, error) {
	if !isBackupOf(path, name) {
		return nil, fmt.Errorf("invalid backup name %q", name)
	}
	data, err := os.ReadFile(filepath.Join(BackupDir(path), name))
	if err != nil {
		return nil, fmt.Errorf("read backup: %w", err)
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse backup: %w", err)
	}
	if err := writeConfig(path, data); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// writeConfig backs up the config at path, then swaps data in through a
// temporary file, so a failed write leaves the old config whole. A config that
// can't be read can't be backed up, so it is left alone and the save fails.
func writeConfig(path string, data []byte) error {
	saveMu.Lock()
	defer saveMu.Unlock()

	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(old, data) {
			return nil
		}
		if err := backup(path, old); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read current config: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".sparks-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	defer os.Remove(tmp.Name()) // fails harmlessly after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// backup stores data as a new backup of the config at path and prunes the
// oldest past backupKeep. A failed prune only leaves an extra file.
func backup(path string, data []byte) error {
	dir := BackupDir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create backup folder: %w", err)
	}
	name := backupPrefix(path) + time.Now().Format(backupStamp) + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	names, err := ListBackups(path)
	if err != nil {
		return nil
	}
	for i := backupKeep; i < len(names); i++ {
		os.Remove(filepath.Join(dir, names[i]))
	}
	return nil
}
