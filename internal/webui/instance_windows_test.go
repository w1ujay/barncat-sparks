//go:build windows

package webui

import (
	"path/filepath"
	"testing"
)

func TestAcquireInstanceSecondCopyRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparks-config.json")
	first, err := acquireInstance(path)
	if err != nil || !first {
		t.Fatalf("first acquire = %v, %v; want true, nil", first, err)
	}
	second, err := acquireInstance(path)
	if err != nil || second {
		t.Fatalf("second acquire = %v, %v; want false, nil", second, err)
	}
}

func TestAcquireInstanceOtherConfigAllowed(t *testing.T) {
	dir := t.TempDir()
	if ok, err := acquireInstance(filepath.Join(dir, "radio1.json")); err != nil || !ok {
		t.Fatalf("radio1 acquire = %v, %v; want true, nil", ok, err)
	}
	if ok, err := acquireInstance(filepath.Join(dir, "radio2.json")); err != nil || !ok {
		t.Fatalf("radio2 acquire = %v, %v; want true, nil", ok, err)
	}
}
