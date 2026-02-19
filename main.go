// wk4 - WinKeyer emulator for Elecraft K4 remote operation
//
// Presents a K1EL WinKeyer3-compatible interface on a serial port,
// translating Winkeyer protocol commands into K4 CAT commands (KY, KS)
// sent via TCP to a CAT server (e.g., QK4 on port 9299).
//
// This allows N1MM Logger and other contest software to use their native
// CW keying engine instead of the limited KY/CATA1ASC approach.
//
// Setup:
//  1. Install com0com (Windows) or use socat (Linux) to create a virtual serial port pair
//  2. Point N1MM's Winkeyer config at one end of the pair
//  3. Point wk4 at the other end: wk4 -port COM5 -cat localhost:9299

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	portName := flag.String("port", "", "Serial port for Winkeyer interface (e.g., COM5, /dev/pts/3)")
	catAddr := flag.String("cat", "localhost:9299", "CAT server address (host:port)")
	speed := flag.Int("speed", 25, "Initial keyer speed in WPM")
	flag.Parse()

	if *portName == "" {
		fmt.Fprintln(os.Stderr, "wk4 - WinKeyer emulator for K4 remote CW keying")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Usage: wk4 -port <serial-port> [-cat host:port] [-speed wpm]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  wk4 -port COM5                    # Windows with com0com")
		fmt.Fprintln(os.Stderr, "  wk4 -port /dev/pts/3 -cat k4:9299 # Linux with socat")
		os.Exit(1)
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("wk4: WinKeyer emulator for K4")
	log.Printf("  serial: %s", *portName)
	log.Printf("  CAT:    %s", *catAddr)
	log.Printf("  speed:  %d WPM", *speed)

	cat := NewCATClient(*catAddr)
	wk := NewWinkeyer(cat)
	wk.wpm = *speed

	if err := wk.OpenSerial(*portName); err != nil {
		log.Fatalf("Failed to open serial port %s: %v", *portName, err)
	}
	defer wk.Close()

	if err := cat.Connect(); err != nil {
		log.Fatalf("Failed to connect to CAT server %s: %v", *catAddr, err)
	}
	defer cat.Close()

	// Set initial speed on K4
	cat.SetSpeed(*speed)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go wk.Run()

	<-sig
	log.Println("wk4: shutting down")
}
