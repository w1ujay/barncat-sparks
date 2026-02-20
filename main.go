// wk4 - WinKeyer emulator and MIDI controller for Elecraft K4
//
// Presents a K1EL WinKeyer3-compatible interface on a serial port,
// translating Winkeyer protocol commands into K4 CAT commands (KY, KS)
// sent via TCP to a CAT server (e.g., QK4 on port 9299).
//
// Optionally connects a MIDI controller (e.g., DJ2GO2) to control
// K4 functions like VFO tuning, volume, band switching, and PTT.
//
// Setup:
//  1. Install com0com (Windows) or use socat (Linux) to create a virtual serial port pair
//  2. Point N1MM's Winkeyer config at one end of the pair
//  3. Point wk4 at the other end: wk4 -port COM5 -cat localhost:9299
//  4. Optionally add MIDI: wk4 -port COM5 -midi DJ2GO2

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
	midiDevice := flag.String("midi", "", "MIDI device name or substring (e.g., DJ2GO2)")
	midiConfigPath := flag.String("midi-config", "midi-config.json", "Path to MIDI mapping config file")
	midiList := flag.Bool("midi-list", false, "List available MIDI devices and exit")
	flag.Parse()

	// Handle -midi-list
	if *midiList {
		if err := ListMIDIDevices(); err != nil {
			log.Fatalf("MIDI: %v", err)
		}
		return
	}

	if *portName == "" && *midiDevice == "" {
		fmt.Fprintln(os.Stderr, "wk4 - WinKeyer emulator & MIDI controller for K4")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Usage: wk4 [-port <serial-port>] [-midi <device>] [-cat host:port]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fmt.Fprintln(os.Stderr, "  -port         Serial port for Winkeyer (e.g., COM5, /dev/pts/3)")
		fmt.Fprintln(os.Stderr, "  -midi         MIDI device name or substring (e.g., DJ2GO2)")
		fmt.Fprintln(os.Stderr, "  -midi-config  Path to MIDI mapping config (default: midi-config.json)")
		fmt.Fprintln(os.Stderr, "  -midi-list    List available MIDI devices and exit")
		fmt.Fprintln(os.Stderr, "  -cat          CAT server address (default: localhost:9299)")
		fmt.Fprintln(os.Stderr, "  -speed        Initial keyer speed in WPM (default: 25)")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  wk4 -port COM5                              # WinKeyer only")
		fmt.Fprintln(os.Stderr, "  wk4 -midi DJ2GO2                            # MIDI only")
		fmt.Fprintln(os.Stderr, "  wk4 -port COM5 -midi DJ2GO2 -cat k4:9299    # Both")
		fmt.Fprintln(os.Stderr, "  wk4 -midi-list                              # List MIDI devices")
		os.Exit(1)
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("wk4: WinKeyer emulator & MIDI controller for K4")
	log.Printf("  CAT: %s", *catAddr)

	cat := NewCATClient(*catAddr)
	if err := cat.Connect(); err != nil {
		log.Fatalf("Failed to connect to CAT server %s: %v", *catAddr, err)
	}
	defer cat.Close()

	// Start WinKeyer if serial port specified
	if *portName != "" {
		log.Printf("  serial: %s", *portName)
		log.Printf("  speed:  %d WPM", *speed)
		wk := NewWinkeyer(cat)
		wk.wpm = *speed
		if err := wk.OpenSerial(*portName); err != nil {
			log.Fatalf("Failed to open serial port %s: %v", *portName, err)
		}
		defer wk.Close()
		cat.SetSpeed(*speed)
		go wk.Run()
	}

	// Start MIDI if device specified
	if *midiDevice != "" {
		midiCfg, err := LoadMIDIConfig(*midiConfigPath)
		if err != nil {
			log.Fatalf("Failed to load MIDI config %s: %v", *midiConfigPath, err)
		}
		midiCfg.Device = *midiDevice
		midi := NewMIDIDriver(cat, midiCfg)
		go func() {
			if err := midi.Run(); err != nil {
				log.Fatalf("MIDI: %v", err)
			}
		}()
		defer midi.Close()
		log.Printf("  MIDI:  %s (config: %s)", *midiDevice, *midiConfigPath)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("wk4: shutting down")
}
