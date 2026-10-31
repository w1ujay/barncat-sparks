//go:build windows

package webui

import (
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/jchv/go-webview2"
)

// waitForGUI blocks until the window closes, a signal arrives or quit closes.
func waitForGUI(url, title string, width, height int, quit <-chan struct{}) {
	runtime.LockOSThread()

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(width),
			Height: uint(height),
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		// WebView2 runtime not installed — fall back to default browser
		log.Println("webui: WebView2 not available, falling back to default browser")
		go openBrowser(url)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
			log.Println("sparks: signal received, shutting down")
		case <-quit:
		}
		return
	}
	defer w.Destroy()

	// Ctrl+C or quit → Terminate(), which unblocks Run(). It posts WM_QUIT to the
	// calling thread, so it has to run on the UI thread, through Dispatch.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			log.Println("sparks: signal received, shutting down")
		case <-quit:
		}
		w.Dispatch(w.Terminate)
	}()

	w.Navigate(url)
	w.Run() // blocks on Win32 message loop until window closed or Terminate()
}
