//go:build !windows

package update

import "time"

// WaitForExit does nothing: only the Windows build updates itself.
func WaitForExit(pid int, timeout time.Duration) {}
