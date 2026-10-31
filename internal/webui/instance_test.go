package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstanceMutexNameSameConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sparks-config.json")
	dotted := dir + string(filepath.Separator) + "." + string(filepath.Separator) + "sparks-config.json"
	upper := filepath.Join(dir, "SPARKS-CONFIG.JSON")
	if instanceMutexName(path) != instanceMutexName(dotted) {
		t.Error("the same file reached through a different spelling must share a lock")
	}
	if instanceMutexName(path) != instanceMutexName(upper) {
		t.Error("Windows paths are case-insensitive, so case must not split the lock")
	}
}

func TestInstanceMutexNameRelativeMatchesAbsolute(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if instanceMutexName("sparks-config.json") != instanceMutexName(filepath.Join(wd, "sparks-config.json")) {
		t.Error("a relative path must share a lock with its absolute form")
	}
}

func TestInstanceMutexNameDifferentConfig(t *testing.T) {
	dir := t.TempDir()
	a := instanceMutexName(filepath.Join(dir, "radio1.json"))
	b := instanceMutexName(filepath.Join(dir, "radio2.json"))
	if a == b {
		t.Error("different configs must get different locks, so a second radio can run its own copy")
	}
}

func TestInstanceMutexNameIsValid(t *testing.T) {
	name := instanceMutexName(`C:\Sparks\sparks-config.json`)
	rest, ok := strings.CutPrefix(name, `Local\BarnCatSparks-`)
	if !ok {
		t.Fatalf("name %q lacks the Local namespace prefix", name)
	}
	if strings.Contains(rest, `\`) {
		t.Errorf("name %q has a backslash after the namespace, which CreateMutex rejects", name)
	}
}
