// wk4 - WinKeyer emulator and MIDI controller for Elecraft K4
//
// Presents a K1EL WinKeyer3-compatible interface on a serial port,
// translating Winkeyer protocol commands into K4 CAT commands (KY, KS)
// sent via TCP to a CAT server (e.g., QK4 on port 9299).
//
// Optionally connects a MIDI controller (e.g., DJ2GO2) to control
// K4 functions like VFO tuning, volume, band switching, and PTT.
//
// Modes:
//   - GUI (default): Double-click to launch. Configure serial port, CAT address,
//     MIDI device, and mappings in a single window. Save settings to wk4-config.json.
//   - Headless (CLI flags): wk4 -port COM5 -midi DJ2GO2 (backwards compatible)

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

const defaultConfigFile = "wk4-config.json"

func main() {
	portName := flag.String("port", "", "Serial port for Winkeyer interface (e.g., COM5, /dev/pts/3)")
	catAddr := flag.String("cat", "", "CAT server address (host:port)")
	speed := flag.Int("speed", 0, "Initial keyer speed in WPM")
	midiDevice := flag.String("midi", "", "MIDI device name or substring (e.g., DJ2GO2)")
	midiConfigPath := flag.String("midi-config", "", "Path to MIDI mapping config file")
	midiList := flag.Bool("midi-list", false, "List available MIDI devices and exit")
	configFile := flag.String("config", "", "Path to config file (default: wk4-config.json next to executable)")
	noGUI := flag.Bool("nogui", false, "Run in headless mode (no GUI)")
	flag.Parse()

	// Handle -midi-list
	if *midiList {
		if err := ListMIDIDevices(); err != nil {
			log.Fatalf("MIDI: %v", err)
		}
		return
	}

	// Determine config file path
	cfgPath := *configFile
	if cfgPath == "" {
		exe, err := os.Executable()
		if err != nil {
			cfgPath = defaultConfigFile
		} else {
			cfgPath = filepath.Join(filepath.Dir(exe), defaultConfigFile)
		}
	}

	// If CLI flags specify port or midi, run headless (backwards compatible)
	if *portName != "" || *midiDevice != "" || *noGUI {
		runHeadless(*portName, *catAddr, *speed, *midiDevice, *midiConfigPath, cfgPath)
		return
	}

	// Otherwise, launch GUI
	RunGUI(cfgPath)
}

func runHeadless(portName, catAddr string, speed int, midiDevice, midiConfigPath, cfgPath string) {
	// Load config for defaults if CLI flags are sparse
	cfg, _ := LoadAppConfig(cfgPath)
	if cfg == nil {
		cfg = DefaultAppConfig()
	}

	// CLI flags override config
	if catAddr == "" {
		catAddr = cfg.CATAddress
	}
	if speed == 0 {
		speed = cfg.KeyerSpeed
	}
	if midiConfigPath == "" && midiDevice != "" {
		midiConfigPath = "midi-config.json"
	}

	if portName == "" && midiDevice == "" {
		fmt.Fprintln(os.Stderr, "wk4 - WinKeyer emulator & MIDI controller for K4")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Usage: wk4                                        # Launch GUI")
		fmt.Fprintln(os.Stderr, "       wk4 [-port <serial-port>] [-midi <device>]  # Headless mode")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fmt.Fprintln(os.Stderr, "  -port         Serial port for Winkeyer (e.g., COM5, /dev/pts/3)")
		fmt.Fprintln(os.Stderr, "  -midi         MIDI device name or substring (e.g., DJ2GO2)")
		fmt.Fprintln(os.Stderr, "  -midi-config  Path to MIDI mapping config (default: midi-config.json)")
		fmt.Fprintln(os.Stderr, "  -midi-list    List available MIDI devices and exit")
		fmt.Fprintln(os.Stderr, "  -cat          CAT server address (default: localhost:9299)")
		fmt.Fprintln(os.Stderr, "  -speed        Initial keyer speed in WPM (default: 25)")
		fmt.Fprintln(os.Stderr, "  -config       Path to config file")
		fmt.Fprintln(os.Stderr, "  -nogui        Run in headless mode (no GUI)")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  wk4                                             # GUI mode")
		fmt.Fprintln(os.Stderr, "  wk4 -port COM5                                  # WinKeyer only")
		fmt.Fprintln(os.Stderr, "  wk4 -midi DJ2GO2                                # MIDI only")
		fmt.Fprintln(os.Stderr, "  wk4 -port COM5 -midi DJ2GO2 -cat k4:9299        # Both")
		os.Exit(1)
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("wk4: WinKeyer emulator & MIDI controller for K4 (headless)")
	log.Printf("  CAT: %s", catAddr)

	cat := NewCATClient(catAddr)
	if err := cat.Connect(); err != nil {
		log.Fatalf("Failed to connect to CAT server %s: %v", catAddr, err)
	}
	defer cat.Close()

	if portName != "" {
		log.Printf("  serial: %s", portName)
		log.Printf("  speed:  %d WPM", speed)
		wk := NewWinkeyer(cat)
		wk.wpm = speed
		if err := wk.OpenSerial(portName); err != nil {
			log.Fatalf("Failed to open serial port %s: %v", portName, err)
		}
		defer wk.Close()
		cat.SetSpeed(speed)
		go wk.Run()
	}

	if midiDevice != "" {
		midiCfg, err := LoadMIDIConfig(midiConfigPath)
		if err != nil {
			log.Fatalf("Failed to load MIDI config %s: %v", midiConfigPath, err)
		}
		midiCfg.Device = midiDevice
		midi := NewMIDIDriver(cat, midiCfg)
		go func() {
			if err := midi.Run(); err != nil {
				log.Fatalf("MIDI: %v", err)
			}
		}()
		defer midi.Close()
		log.Printf("  MIDI:  %s (config: %s)", midiDevice, midiConfigPath)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("wk4: shutting down")
}
