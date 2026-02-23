//go:build !windows

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func waitForGUI(url, title string, width, height int) {
	// Try app-mode browser (chromeless window), fall back to default browser
	var browser *appBrowser
	if browserPath := findBrowser(); browserPath != "" {
		dataDir, err := os.MkdirTemp("", "sparks-browser-*")
		if err != nil {
			log.Printf("webui: temp dir failed, falling back to default browser: %v", err)
			go openBrowser(url)
		} else {
			defer os.RemoveAll(dataDir)
			time.Sleep(100 * time.Millisecond) // let server start
			browser, err = launchAppMode(browserPath, url, dataDir, width, height)
			if err != nil {
				log.Printf("webui: app mode failed, falling back to default browser: %v", err)
				go openBrowser(url)
			} else {
				log.Printf("webui: opened app window (%s)", browserPath)
			}
		}
	} else {
		go openBrowser(url)
	}

	// Wait for signal or browser close
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	if browser != nil {
		select {
		case <-sig:
			log.Println("sparks: signal received, shutting down")
			browser.kill()
		case <-browser.done:
			log.Println("sparks: browser closed, shutting down")
		}
	} else {
		<-sig
		log.Println("sparks: signal received, shutting down")
	}
}
