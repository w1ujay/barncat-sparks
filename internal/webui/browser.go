//go:build !windows

package webui

import (
	"os"
	"os/exec"
	"runtime"
	"time"
)

// appBrowser represents a running browser process in --app mode.
type appBrowser struct {
	cmd  *exec.Cmd
	done chan struct{} // closed when the browser process exits
}

// kill terminates the browser process gracefully, then forcefully after a timeout.
func (b *appBrowser) kill() {
	if b.cmd == nil || b.cmd.Process == nil {
		return
	}
	// Try graceful interrupt first (no-op on Windows, but harmless)
	b.cmd.Process.Signal(os.Interrupt)
	select {
	case <-b.done:
		return
	case <-time.After(2 * time.Second):
		b.cmd.Process.Kill()
	}
}

// browserCandidates returns candidate browser binary paths for the current OS.
// Edge is preferred on Windows (preinstalled); Chrome and Chromium are tried on all platforms.
func browserCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("LocalAppData") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("ProgramFiles(x86)") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LocalAppData") + `\Google\Chrome\Application\chrome.exe`,
		}
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default: // linux, etc.
		return []string{
			"microsoft-edge",
			"microsoft-edge-stable",
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
		}
	}
}

// findBrowser returns the path to the first available Chromium-based browser.
func findBrowser() string {
	for _, candidate := range browserCandidates() {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path
		}
		// On Windows/macOS, candidates are absolute paths — check if the file exists
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// launchAppMode starts a Chromium-based browser in --app mode and returns an
// appBrowser that tracks the process lifetime.
func launchAppMode(browserPath, url, userDataDir string, width, height int) (*appBrowser, error) {
	cmd := exec.Command(browserPath,
		"--app="+url,
		"--user-data-dir="+userDataDir,
		"--window-size="+itoa(width)+","+itoa(height),
		"--no-first-run",
		"--disable-extensions",
	)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	b := &appBrowser{cmd: cmd, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(b.done)
	}()
	return b, nil
}

// itoa converts an int to a string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
