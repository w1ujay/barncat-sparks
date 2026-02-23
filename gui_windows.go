//go:build windows

package main

import (
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/jchv/go-webview2"
)

func waitForGUI(url, title string, width, height int) {
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
		<-sig
		log.Println("sparks: signal received, shutting down")
		return
	}
	defer w.Destroy()

	// Signal handler: Ctrl+C → Terminate() (thread-safe, unblocks Run())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("sparks: signal received, shutting down")
		w.Terminate()
	}()

	w.Navigate(url)
	w.Run() // blocks on Win32 message loop until window closed or Terminate()
}
