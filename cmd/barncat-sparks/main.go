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

	"github.com/ujay/barncat-sparks/internal/cat"
	"github.com/ujay/barncat-sparks/internal/config"
	"github.com/ujay/barncat-sparks/internal/controls"
	"github.com/ujay/barncat-sparks/internal/keyer"
	"github.com/ujay/barncat-sparks/internal/midi"
	"github.com/ujay/barncat-sparks/internal/webui"
)

// Version is defined in internal/config/about.go

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
	catPassword := flag.String("cat-password", "", "K4 authentication password (for port 9205)")
	catListen := flag.String("cat-listen", "", "TCP listen address for CAT server (e.g., :9300)")
	radioListen := flag.String("radio-listen", "", "TCP listen address for RadioServer (e.g., :9400)")
	radioModel := flag.String("radio-model", "", "Radio model: k4 or k3 (default k4; k3 needs -cat-transport serial)")
	radioLogRaw := flag.Bool("radio-log-raw", false, "Log every command RadioServer passes through to the radio")
	noGUI := flag.Bool("nogui", false, "Run in headless mode (no GUI)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sparks %s\n", config.Version)
		return
	}

	// Handle -midi-list
	if *midiList {
		if err := midi.ListMIDIDevices(); err != nil {
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
		os.Exit(runHeadless(*portName, *catAddr, *speed, *midiDevice, *midiConfigPath, cfgPath,
			*catTransport, *catSerialPort, *catBaud, *catPassword, *catListen, *radioListen,
			*radioModel, *radioLogRaw))
	}

	// Otherwise, launch web UI
	webui.RunWebUI(cfgPath)
}

// hasHeadlessWork reports whether headless mode has anything to run: a
// WinKeyer port, a MIDI device, or a CATServer or RadioServer on its own.
func hasHeadlessWork(portName, midiDevice, catListen, radioListen string) bool {
	return portName != "" || midiDevice != "" || catListen != "" || radioListen != ""
}

// validateRadioModel checks -radio-model against the transport and CATServer
// settings before anything connects.
func validateRadioModel(model, transport, catListen string) error {
	switch model {
	case "k4":
		return nil
	case "k3":
		if transport != "serial" {
			return fmt.Errorf("-radio-model k3 needs -cat-transport serial")
		}
		if catListen != "" {
			return fmt.Errorf("-radio-model k3 can't be used with -cat-listen: CATServer relays raw bytes and would bypass K3 translation; use -radio-listen")
		}
		return nil
	}
	return fmt.Errorf("unknown radio model %q (want k4 or k3)", model)
}

func runHeadless(portName, catAddr string, speed int, midiDevice, midiConfigPath, cfgPath,
	catTransport, catSerialPort string, catBaud int, catPassword string,
	catListen, radioListen, radioModel string, radioLogRaw bool) int {
	// Load config for defaults if CLI flags are sparse
	cfg, _ := config.LoadAppConfig(cfgPath)
	if cfg == nil {
		cfg = config.DefaultAppConfig()
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
	if catPassword == "" {
		catPassword = cfg.CATPassword
	}
	if catListen == "" {
		catListen = cfg.CATListen
	}
	if radioListen == "" {
		radioListen = cfg.RadioServerListen
	}
	if speed == 0 {
		speed = cfg.KeyerSpeed
	}
	if midiConfigPath == "" && midiDevice != "" {
		midiConfigPath = "midi-config.json"
	}
	if radioModel == "" {
		radioModel = cfg.RadioModel
	}
	if radioModel == "" {
		radioModel = "k4"
	}

	if !hasHeadlessWork(portName, midiDevice, catListen, radioListen) {
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
		fmt.Fprintln(os.Stderr, "  -cat-password   K4 authentication password (for port 9205)")
		fmt.Fprintln(os.Stderr, "  -cat-listen     TCP listen address for CAT server (e.g., :9300)")
		fmt.Fprintln(os.Stderr, "  -radio-listen   TCP listen address for RadioServer (e.g., :9400)")
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
		return 1
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("sparks %s: WinKeyer emulator & MIDI controller for K4 (headless)", config.Version)

	if err := validateRadioModel(radioModel, catTransport, catListen); err != nil {
		log.Print(err)
		return 1
	}

	c := cat.NewCATClient()
	switch catTransport {
	case "serial":
		if catSerialPort == "" {
			log.Fatal("serial transport requires -cat-serial")
		}
		log.Printf("  CAT: %s (serial, %d baud)", catSerialPort, catBaud)
		if err := c.ConnectSerial(catSerialPort, catBaud); err != nil {
			log.Fatalf("Failed to connect to radio: %v", err)
		}
	default:
		if catPassword != "" {
			log.Printf("  CAT: %s (K4 auth)", catAddr)
			if err := c.ConnectK4(catAddr, catPassword); err != nil {
				log.Fatalf("K4 auth failed for %s: %v", catAddr, err)
			}
		} else {
			log.Printf("  CAT: %s (TCP)", catAddr)
			if err := c.ConnectTCP(catAddr); err != nil {
				log.Fatalf("Failed to connect to CAT server %s: %v", catAddr, err)
			}
		}
	}
	defer c.Close()

	var radio cat.Radio = c
	var linkLost <-chan struct{}
	if radioModel == "k3" {
		k3, err := cat.NewK3Radio(c)
		if err != nil {
			log.Printf("K3: %v", err)
			return 1
		}
		defer k3.Close() // runs before c.Close: sends RX, stops the KY sender
		radio = k3
		linkLost = c.Done()
		log.Printf("  radio: K3 (translating K4 dialect)")
	}

	// Start CATServer if listen address configured
	if catListen != "" {
		srv, err := cat.NewCATServer(c, catListen)
		if err != nil {
			log.Fatalf("CATServer: %v", err)
		}
		go srv.Run()
		defer srv.Close()
		log.Printf("  CATServer: %s", catListen)
	}

	// Start RadioServer if listen address configured
	if radioListen != "" {
		rsrv, err := cat.NewRadioServer(radio, radioListen)
		if err != nil {
			log.Fatalf("RadioServer: %v", err)
		}
		rsrv.SetLogRaw(radioLogRaw)
		go rsrv.Run()
		defer rsrv.Close()
		log.Printf("  RadioServer: %s", radioListen)
	}

	if portName != "" {
		log.Printf("  serial: %s", portName)
		log.Printf("  speed:  %d WPM", speed)
		wk := keyer.NewWinkeyer(radio)
		wk.SetWPM(speed)
		if err := wk.OpenSerial(portName); err != nil {
			log.Fatalf("Failed to open serial port %s: %v", portName, err)
		}
		defer wk.Close()
		radio.SetSpeed(speed)
		go wk.Run()
	}

	if midiDevice != "" {
		midiCfg, err := midi.LoadMIDIConfig(midiConfigPath)
		if err != nil {
			log.Fatalf("Failed to load MIDI config %s: %v", midiConfigPath, err)
		}
		midiCfg.Devices = []string{midiDevice}
		exec := controls.New(radio)
		defer exec.Close() // deferred first, so it runs after midiDrv.Close
		midiDrv := midi.NewMIDIDriver(exec, midiCfg)
		go func() {
			if err := midiDrv.Run(); err != nil {
				log.Fatalf("MIDI: %v", err)
			}
		}()
		defer midiDrv.Close()
		log.Printf("  MIDI:  %s (config: %s)", midiDevice, midiConfigPath)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		log.Println("sparks: shutting down")
		return 0
	case <-linkLost:
		log.Println("sparks: radio link lost; exiting so the service manager restarts it")
		return 1
	}
}
