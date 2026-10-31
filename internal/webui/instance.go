package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

const alreadyRunningMsg = "BarnCat Sparks is already running with this config.\n\n" +
	"Use the open window, or start the other copy with its own -config file."

// instanceMutexName returns the lock name for a config file. The lock follows
// the config rather than the machine, so a second radio can run its own copy
// from its own config. A hash keeps backslashes out of the name.
func instanceMutexName(cfgPath string) string {
	if abs, err := filepath.Abs(cfgPath); err == nil {
		cfgPath = abs
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(cfgPath))))
	return `Local\BarnCatSparks-` + hex.EncodeToString(sum[:])
}
