//go:build !windows

package webui

// acquireInstance always succeeds: the single-copy check is Windows-only.
func acquireInstance(cfgPath string) (bool, error) {
	return true, nil
}

func showAlreadyRunning() {}
