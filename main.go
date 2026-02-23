// BarnCat Sparks - WinKeyer emulator and MIDI controller for Elecraft K4
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
//     MIDI device, and mappings in a single window. Save settings to sparks-config.json.
//   - Headless (CLI flags): barncat-sparks -port COM5 -midi DJ2GO2 (backwards compatible)

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

// Version is defined in about.go

const defaultConfigFile = "sparks-config.json"

func main() {
	portName := flag.String("port", "", "Serial port for Winkeyer interface (e.g., COM5, /dev/pts/3)")
	catAddr := flag.String("cat", "", "CAT server address (host:port)")
	speed := flag.Int("speed", 0, "Initial keyer speed in WPM")
	midiDevice := flag.String("midi", "", "MIDI device name or substring (e.g., DJ2GO2)")
	midiConfigPath := flag.String("midi-config", "", "Path to MIDI mapping config file")
	midiList := flag.Bool("midi-list", false, "List available MIDI devices and exit")
	configFile := flag.String("config", "", "Path to config file (default: sparks-config.json next to executable)")
	catTransport := flag.String("cat-transport", "", "CAT transport: tcp or serial")
	catSerialPort := flag.String("cat-serial", "", "Serial port for radio CAT (e.g., COM3)")
	catBaud := flag.Int("cat-baud", 0, "Baud rate for serial CAT (default: 38400)")
	catListen := flag.String("cat-listen", "", "TCP listen address for CAT server (e.g., :9300)")
	noGUI := flag.Bool("nogui", false, "Run in headless mode (no GUI)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sparks %s\n", Version)
		return
	}

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
		runHeadless(*portName, *catAddr, *speed, *midiDevice, *midiConfigPath, cfgPath,
			*catTransport, *catSerialPort, *catBaud, *catListen)
		return
	}

	// Otherwise, launch web UI
	RunWebUI(cfgPath)
}

func runHeadless(portName, catAddr string, speed int, midiDevice, midiConfigPath, cfgPath,
	catTransport, catSerialPort string, catBaud int, catListen string) {
	// Load config for defaults if CLI flags are sparse
	cfg, _ := LoadAppConfig(cfgPath)
	if cfg == nil {
		cfg = DefaultAppConfig()
	}

	// CLI flags override config
	if catAddr == "" {
		catAddr = cfg.CATAddress
	}
	if catTransport == "" {
		catTransport = cfg.CATTransport
	}
	if catTransport == "" {
		catTransport = "tcp"
	}
	if catSerialPort == "" {
		catSerialPort = cfg.CATSerialPort
	}
	if catBaud == 0 {
		catBaud = cfg.CATSerialBaud
	}
	if catBaud == 0 {
		catBaud = 38400
	}
	if catListen == "" {
		catListen = cfg.CATListen
	}
	if speed == 0 {
		speed = cfg.KeyerSpeed
	}
	if midiConfigPath == "" && midiDevice != "" {
		midiConfigPath = "midi-config.json"
	}

	if portName == "" && midiDevice == "" {
		fmt.Fprintln(os.Stderr, "BarnCat Sparks - WinKeyer emulator & MIDI controller for K4")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Usage: barncat-sparks                                        # Launch web UI")
		fmt.Fprintln(os.Stderr, "       barncat-sparks [-port <serial-port>] [-midi <device>]  # Headless mode")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fmt.Fprintln(os.Stderr, "  -port           Serial port for Winkeyer (e.g., COM5, /dev/pts/3)")
		fmt.Fprintln(os.Stderr, "  -midi           MIDI device name or substring (e.g., DJ2GO2)")
		fmt.Fprintln(os.Stderr, "  -midi-config    Path to MIDI mapping config (default: midi-config.json)")
		fmt.Fprintln(os.Stderr, "  -midi-list      List available MIDI devices and exit")
		fmt.Fprintln(os.Stderr, "  -cat            CAT server address (default: localhost:9299)")
		fmt.Fprintln(os.Stderr, "  -cat-transport  CAT transport: tcp or serial (default: tcp)")
		fmt.Fprintln(os.Stderr, "  -cat-serial     Serial port for radio CAT (e.g., COM3)")
		fmt.Fprintln(os.Stderr, "  -cat-baud       Baud rate for serial CAT (default: 38400)")
		fmt.Fprintln(os.Stderr, "  -cat-listen     TCP listen address for CAT server (e.g., :9300)")
		fmt.Fprintln(os.Stderr, "  -speed          Initial keyer speed in WPM (default: 25)")
		fmt.Fprintln(os.Stderr, "  -config         Path to config file")
		fmt.Fprintln(os.Stderr, "  -nogui          Run in headless mode (no web UI)")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  barncat-sparks                                             # Web UI mode")
		fmt.Fprintln(os.Stderr, "  barncat-sparks -port COM5                                  # WinKeyer only")
		fmt.Fprintln(os.Stderr, "  barncat-sparks -midi DJ2GO2                                # MIDI only")
		fmt.Fprintln(os.Stderr, "  barncat-sparks -port COM5 -midi DJ2GO2 -cat k4:9299        # Both")
		fmt.Fprintln(os.Stderr, "  barncat-sparks -cat-transport serial -cat-serial COM3 -cat-listen :9300  # Bridge")
		os.Exit(1)
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("sparks %s: WinKeyer emulator & MIDI controller for K4 (headless)", Version)

	cat := NewCATClient()
	switch catTransport {
	case "serial":
		if catSerialPort == "" {
			log.Fatal("serial transport requires -cat-serial")
		}
		log.Printf("  CAT: %s (serial, %d baud)", catSerialPort, catBaud)
		if err := cat.ConnectSerial(catSerialPort, catBaud); err != nil {
			log.Fatalf("Failed to connect to radio: %v", err)
		}
	default:
		log.Printf("  CAT: %s (TCP)", catAddr)
		if err := cat.ConnectTCP(catAddr); err != nil {
			log.Fatalf("Failed to connect to CAT server %s: %v", catAddr, err)
		}
	}
	defer cat.Close()

	// Start CATServer if listen address configured
	if catListen != "" {
		srv, err := NewCATServer(cat, catListen)
		if err != nil {
			log.Fatalf("CATServer: %v", err)
		}
		go srv.Run()
		defer srv.Close()
		log.Printf("  CATServer: %s", catListen)
	}

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
	log.Println("sparks: shutting down")
}
