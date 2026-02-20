package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// GUI holds all the GUI state and widgets.
type GUI struct {
	app    fyne.App
	window fyne.Window
	config *AppConfig

	// Config file path
	cfgPath string

	// Connection section
	serialPortSelect *widget.Select
	catAddrEntry     *widget.Entry
	speedEntry       *widget.Entry

	// MIDI section
	midiDeviceSelect *widget.Select
	mappingRows      []*MappingRow

	// Controls
	startBtn  *widget.Button
	stopBtn   *widget.Button
	saveBtn   *widget.Button
	statusLbl *widget.Label

	// Options
	trayCheck *widget.Check

	// Mapping container (rebuilt when rows added/removed)
	mappingContainer *fyne.Container

	// Running state
	running bool
	cat     *CATClient
	wk      *Winkeyer
	midi    *MIDIDriver
}

// MappingRow holds the widgets for a single mapping row.
type MappingRow struct {
	actionSelect  *widget.Select
	typeSelect    *widget.Select
	channelEntry  *widget.Entry
	numberEntry   *widget.Entry
	optsContainer *fyne.Container
	optsWidgets   map[string]*widget.Entry
	learnBtn      *widget.Button
}

// RunGUI starts the GUI application.
func RunGUI(configPath string) {
	cfg, err := LoadAppConfig(configPath)
	if err != nil {
		cfg = DefaultAppConfig()
	}

	g := &GUI{config: cfg, cfgPath: configPath}
	g.app = app.NewWithID("com.wk4.app")
	g.app.Settings().SetTheme(theme.DarkTheme())
	g.window = g.app.NewWindow("wk4 — WinKeyer & MIDI Controller")
	g.window.Resize(fyne.NewSize(620, 700))

	g.buildUI()
	g.loadConfigIntoUI()

	g.window.SetCloseIntercept(func() {
		g.stopAll()
		g.window.Close()
	})

	g.window.ShowAndRun()
}

func (g *GUI) buildUI() {
	// --- Connection section ---
	g.serialPortSelect = widget.NewSelect([]string{}, nil)
	g.serialPortSelect.PlaceHolder = "(none)"
	g.refreshSerialPorts()

	g.catAddrEntry = widget.NewEntry()
	g.catAddrEntry.SetPlaceHolder("localhost:9299")

	g.speedEntry = widget.NewEntry()
	g.speedEntry.SetPlaceHolder("25")

	connSection := container.NewVBox(
		widget.NewLabelWithStyle("Connection", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.New(layout.NewFormLayout(),
			widget.NewLabel("Serial Port:"), g.serialPortSelect,
			widget.NewLabel("K4 Address:"), g.catAddrEntry,
			widget.NewLabel("Keyer Speed (WPM):"), g.speedEntry,
		),
	)

	// --- MIDI section ---
	g.midiDeviceSelect = widget.NewSelect([]string{}, nil)
	g.midiDeviceSelect.PlaceHolder = "(no MIDI devices)"
	g.refreshMIDIDevices()

	g.mappingContainer = container.NewVBox()
	g.rebuildMappingTable()

	addBtn := widget.NewButton("+ Add", func() { g.addMappingRow(nil) })
	removeBtn := widget.NewButton("- Remove", func() { g.removeLastMappingRow() })

	midiSection := container.NewVBox(
		widget.NewLabelWithStyle("MIDI", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.New(layout.NewFormLayout(),
			widget.NewLabel("Device:"), g.midiDeviceSelect,
		),
		widget.NewLabel("Mappings:"),
		g.mappingContainer,
		container.NewHBox(addBtn, removeBtn),
	)

	// --- Options section ---
	g.trayCheck = widget.NewCheck("Minimize to system tray", nil)

	optionsSection := container.NewVBox(
		widget.NewLabelWithStyle("Options", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		g.trayCheck,
	)

	// --- Controls ---
	g.startBtn = widget.NewButton("Start", func() { g.start() })
	g.stopBtn = widget.NewButton("Stop", func() { g.stop() })
	g.stopBtn.Disable()
	g.saveBtn = widget.NewButton("Save Settings", func() { g.saveConfig() })

	g.statusLbl = widget.NewLabel("Status: Stopped")

	controls := container.NewHBox(
		g.startBtn, g.stopBtn,
		layout.NewSpacer(),
		g.saveBtn,
	)

	// --- Assemble ---
	content := container.NewVBox(
		connSection,
		widget.NewSeparator(),
		midiSection,
		widget.NewSeparator(),
		optionsSection,
		widget.NewSeparator(),
		controls,
		g.statusLbl,
	)

	g.window.SetContent(container.NewScroll(content))
}

// refreshSerialPorts populates the serial port dropdown.
func (g *GUI) refreshSerialPorts() {
	ports := listSerialPorts()
	g.serialPortSelect.Options = ports
	g.serialPortSelect.Refresh()
}

// refreshMIDIDevices populates the MIDI device dropdown.
func (g *GUI) refreshMIDIDevices() {
	devices := listMIDIDeviceNames()
	g.midiDeviceSelect.Options = devices
	g.midiDeviceSelect.Refresh()
}

// loadConfigIntoUI populates the UI from the loaded config.
func (g *GUI) loadConfigIntoUI() {
	if g.config.SerialPort != "" {
		g.serialPortSelect.SetSelected(g.config.SerialPort)
	}
	g.catAddrEntry.SetText(g.config.CATAddress)
	g.speedEntry.SetText(fmt.Sprintf("%d", g.config.KeyerSpeed))
	if g.config.MIDIDevice != "" {
		g.midiDeviceSelect.SetSelected(g.config.MIDIDevice)
	}
	g.trayCheck.SetChecked(g.config.MinimizeToTray)

	// Load mappings
	g.mappingRows = nil
	for _, m := range g.config.Mappings {
		g.addMappingRow(&m)
	}
	g.rebuildMappingTable()
}

// addMappingRow adds a new mapping row, optionally pre-filled.
func (g *GUI) addMappingRow(entry *MappingEntry) {
	row := &MappingRow{
		optsWidgets: make(map[string]*widget.Entry),
	}

	actionNames := ActionNames()
	labels := make([]string, len(actionNames))
	for i, name := range actionNames {
		labels[i] = ActionRegistry[name].Label
	}

	row.actionSelect = widget.NewSelect(labels, func(label string) {
		g.onActionChanged(row, label)
	})

	row.typeSelect = widget.NewSelect([]string{"CC", "Note On"}, nil)
	row.channelEntry = widget.NewEntry()
	row.channelEntry.SetText("0")
	row.numberEntry = widget.NewEntry()
	row.numberEntry.SetText("0")
	row.optsContainer = container.NewHBox()
	row.learnBtn = widget.NewButton("Learn", func() {
		g.startLearn(row)
	})

	if entry != nil {
		if def, ok := ActionRegistry[entry.Action]; ok {
			row.actionSelect.SetSelected(def.Label)
		}
		switch entry.Type {
		case "cc":
			row.typeSelect.SetSelected("CC")
		case "note_on":
			row.typeSelect.SetSelected("Note On")
		}
		row.channelEntry.SetText(fmt.Sprintf("%d", entry.Channel))
		row.numberEntry.SetText(fmt.Sprintf("%d", entry.Number))
		g.populateOpts(row, entry.Action, entry.Opts)
	}

	g.mappingRows = append(g.mappingRows, row)
	g.rebuildMappingTable()
}

// removeLastMappingRow removes the last mapping row.
func (g *GUI) removeLastMappingRow() {
	if len(g.mappingRows) > 0 {
		g.mappingRows = g.mappingRows[:len(g.mappingRows)-1]
		g.rebuildMappingTable()
	}
}

// rebuildMappingTable rebuilds the mapping UI from current rows.
func (g *GUI) rebuildMappingTable() {
	g.mappingContainer.RemoveAll()

	if len(g.mappingRows) == 0 {
		g.mappingContainer.Add(widget.NewLabel("(no mappings — click '+ Add' to create one)"))
		return
	}

	// Header
	header := container.NewGridWithColumns(6,
		widget.NewLabelWithStyle("Action", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Type", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Ch", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Number", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Options", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{}),
	)
	g.mappingContainer.Add(header)

	for _, row := range g.mappingRows {
		rowContainer := container.NewGridWithColumns(6,
			row.actionSelect,
			row.typeSelect,
			row.channelEntry,
			row.numberEntry,
			row.optsContainer,
			row.learnBtn,
		)
		g.mappingContainer.Add(rowContainer)
	}
}

// onActionChanged updates the opts column when the action dropdown changes.
func (g *GUI) onActionChanged(row *MappingRow, label string) {
	// Find the action name from label
	actionName := ""
	for name, def := range ActionRegistry {
		if def.Label == label {
			actionName = name
			break
		}
	}
	g.populateOpts(row, actionName, nil)
	g.rebuildMappingTable()
}

// populateOpts fills in the option widgets for a row based on the action.
func (g *GUI) populateOpts(row *MappingRow, actionName string, existingOpts map[string]interface{}) {
	row.optsContainer.RemoveAll()
	row.optsWidgets = make(map[string]*widget.Entry)

	def, ok := ActionRegistry[actionName]
	if !ok || len(def.Options) == 0 {
		return
	}

	for _, opt := range def.Options {
		entry := widget.NewEntry()
		entry.SetText(opt.Default)
		if existingOpts != nil {
			if val, ok := existingOpts[opt.Key]; ok {
				entry.SetText(fmt.Sprintf("%v", val))
			}
		}
		row.optsWidgets[opt.Key] = entry
		row.optsContainer.Add(widget.NewLabel(opt.Label + ":"))
		row.optsContainer.Add(entry)
	}
}

// startLearn starts MIDI learn mode for a row.
// Placeholder — will be implemented in a later task.
func (g *GUI) startLearn(row *MappingRow) {
	row.learnBtn.SetText("Listening...")
	row.learnBtn.Disable()
	// TODO: Wire to MIDI event capture
}

// start connects all subsystems.
func (g *GUI) start() {
	g.readUIIntoConfig()

	// Save config on start
	if err := SaveAppConfig(g.configPath(), g.config); err != nil {
		g.setStatus(fmt.Sprintf("Save failed: %v", err))
	}

	g.setStatus("Connecting to K4...")

	// Connect CAT
	g.cat = NewCATClient(g.config.CATAddress)
	if err := g.cat.Connect(); err != nil {
		g.setStatus(fmt.Sprintf("CAT error: %v", err))
		return
	}

	// Start WinKeyer if serial port selected
	if g.config.SerialPort != "" {
		g.wk = NewWinkeyer(g.cat)
		g.wk.wpm = g.config.KeyerSpeed
		if err := g.wk.OpenSerial(g.config.SerialPort); err != nil {
			g.setStatus(fmt.Sprintf("Serial error: %v", err))
			g.cat.Close()
			g.cat = nil
			return
		}
		g.cat.SetSpeed(g.config.KeyerSpeed)
		go g.wk.Run()
	}

	// Start MIDI if device selected
	if g.config.MIDIDevice != "" {
		midiCfg := g.config.ToMIDIConfig()
		g.midi = NewMIDIDriver(g.cat, midiCfg)
		go func() {
			if err := g.midi.Run(); err != nil {
				// Update status on UI thread
				g.setStatus(fmt.Sprintf("MIDI: %v", err))
			}
		}()
	}

	g.running = true
	g.startBtn.Disable()
	g.stopBtn.Enable()

	status := fmt.Sprintf("Running — CAT: %s", g.config.CATAddress)
	if g.config.SerialPort != "" {
		status += fmt.Sprintf(", Serial: %s", g.config.SerialPort)
	}
	if g.config.MIDIDevice != "" {
		status += fmt.Sprintf(", MIDI: %s", g.config.MIDIDevice)
	}
	g.setStatus(status)
}

// stop disconnects all subsystems.
func (g *GUI) stop() {
	if g.midi != nil {
		g.midi.Close()
		g.midi = nil
	}
	if g.wk != nil {
		g.wk.Close()
		g.wk = nil
	}
	if g.cat != nil {
		g.cat.Close()
		g.cat = nil
	}
	g.running = false
	g.startBtn.Enable()
	g.stopBtn.Disable()
	g.setStatus("Stopped")
}

// stopAll stops everything on window close.
func (g *GUI) stopAll() {
	if g.running {
		g.stop()
	}
}

// saveConfig reads UI state back into the config and saves it.
func (g *GUI) saveConfig() {
	g.readUIIntoConfig()
	path := g.configPath()
	if err := SaveAppConfig(path, g.config); err != nil {
		g.setStatus(fmt.Sprintf("Save failed: %v", err))
		return
	}
	g.setStatus("Settings saved")
}

// readUIIntoConfig reads all UI widget values into g.config.
func (g *GUI) readUIIntoConfig() {
	g.config.SerialPort = g.serialPortSelect.Selected
	g.config.CATAddress = g.catAddrEntry.Text
	if g.config.CATAddress == "" {
		g.config.CATAddress = "localhost:9299"
	}

	speed := 25
	fmt.Sscanf(g.speedEntry.Text, "%d", &speed)
	if speed < 5 {
		speed = 5
	}
	if speed > 99 {
		speed = 99
	}
	g.config.KeyerSpeed = speed

	g.config.MIDIDevice = g.midiDeviceSelect.Selected
	g.config.MinimizeToTray = g.trayCheck.Checked

	g.config.Mappings = nil
	for _, row := range g.mappingRows {
		entry := g.rowToMappingEntry(row)
		if entry.Action != "" {
			g.config.Mappings = append(g.config.Mappings, entry)
		}
	}
}

// rowToMappingEntry converts a GUI mapping row to a MappingEntry.
func (g *GUI) rowToMappingEntry(row *MappingRow) MappingEntry {
	entry := MappingEntry{}

	// Find action name from label
	for name, def := range ActionRegistry {
		if def.Label == row.actionSelect.Selected {
			entry.Action = name
			break
		}
	}

	switch row.typeSelect.Selected {
	case "CC":
		entry.Type = "cc"
	case "Note On":
		entry.Type = "note_on"
	}

	fmt.Sscanf(row.channelEntry.Text, "%d", &entry.Channel)
	fmt.Sscanf(row.numberEntry.Text, "%d", &entry.Number)

	if len(row.optsWidgets) > 0 {
		entry.Opts = make(map[string]interface{})
		for key, w := range row.optsWidgets {
			var val float64
			if _, err := fmt.Sscanf(w.Text, "%f", &val); err == nil {
				entry.Opts[key] = val
			} else {
				entry.Opts[key] = w.Text
			}
		}
	}

	return entry
}

// configPath returns the config file path.
func (g *GUI) configPath() string {
	return g.cfgPath
}

func (g *GUI) setStatus(msg string) {
	g.statusLbl.SetText("Status: " + msg)
}
