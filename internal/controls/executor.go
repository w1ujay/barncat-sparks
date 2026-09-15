// Package controls runs radio actions for hands-on inputs (MIDI controllers,
// the USB knob). Inputs turn their messages into Events; the Executor runs one
// Event at a time and owns the state actions share: the VFO cache and throttle,
// toggles, RF gain and filter levels, and PTT.
package controls

import (
	"log"
	"sync"
	"time"

	"github.com/ujay/barncat-sparks/internal/cat"
)

// Event asks the executor to run one action.
type Event struct {
	Action string
	Opts   map[string]any
	// Value is MIDI-style 0-127: an absolute position for absolute actions, a
	// two's-complement step count for relative actions (1 = +1 step,
	// 127 = -1 step), and ignored by button actions.
	Value int
	// Release marks a PTT release (MIDI note off). Other actions ignore it.
	Release bool
}

// Executor runs Events against the radio. Handle is synchronous and holds mu
// for the whole event, so events from several inputs run one at a time in
// arrival order: PTT press and release reach the radio in order, and tuning
// state is never touched concurrently.
type Executor struct {
	mu         sync.Mutex
	cat        cat.Radio
	volumeCat  cat.Radio // optional: route volume commands here (e.g. QK4)
	paddleSwap bool

	pttOn  bool
	closed bool // set by Close; PTT is never keyed after it

	// VFO tuning state
	vfoFreq     int
	vfoKnown    bool
	lastTuneAt  time.Time
	vfoBFreq    int
	vfoBKnown   bool
	lastTuneBAt time.Time

	// Toggle state for button actions
	ritOn    bool
	xitOn    bool
	nbOn     bool
	nrOn     bool
	splitOn  bool
	notchOn  bool
	notchHz  int // current notch frequency in Hz (0 = unknown)
	filterBW int // current filter BW in Hz (0 = unknown, default 500)

	// RF gain state
	rfGain  int // main RX attenuation level (0-60, 0=full gain)
	rfGainB int // sub RX attenuation level
}

// New creates an executor for radio. paddleSwap swaps dit and dah for the
// paddle actions.
func New(radio cat.Radio, paddleSwap bool) *Executor {
	return &Executor{cat: radio, paddleSwap: paddleSwap}
}

// SetVolumeTarget sets an alternative Radio for volume commands. When set,
// volume and volume_b go here instead of the main radio.
func (e *Executor) SetVolumeTarget(r cat.Radio) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.volumeCat = r
}

// volumeTarget returns the Radio to use for volume commands. Must be called
// with e.mu held.
func (e *Executor) volumeTarget() cat.Radio {
	if e.volumeCat != nil {
		return e.volumeCat
	}
	return e.cat
}

// Handle runs one event.
func (e *Executor) Handle(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ev.Release {
		if ev.Action == "ptt" && e.pttOn {
			log.Printf("controls: ptt off")
			e.pttOn = false
			// Synchronous, like key-down, so release follows press.
			e.cat.SetPTT(false)
		}
		return
	}
	e.dispatch(ev)
}

// DeviceLost is called when an input device goes away (USB unplug). It unkeys
// the radio if a PTT is held, because that control's release can never arrive.
// The executor stays open, so PTT keys again once the device is back.
func (e *Executor) DeviceLost(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pttOn {
		log.Printf("controls: releasing PTT, device %q disconnected", name)
		e.pttOn = false
		e.cat.SetPTT(false)
	}
}

// Close marks the executor closed and unkeys the radio if a PTT is held. A PTT
// press handled after Close does nothing. Safe to call more than once.
func (e *Executor) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	if e.pttOn {
		log.Printf("controls: releasing PTT on close")
		e.pttOn = false
		e.cat.SetPTT(false)
	}
}

// optFloat returns a float64 option from an opts map, or the fallback.
func optFloat(opts map[string]any, key string, fallback float64) float64 {
	if opts == nil {
		return fallback
	}
	v, ok := opts[key]
	if !ok {
		return fallback
	}
	f, ok := v.(float64)
	if !ok {
		return fallback
	}
	return f
}

// ccValue scales a MIDI CC value (0-127) to an output range, with optional invert.
func ccValue(value, maxOut int, opts map[string]any) int {
	if optFloat(opts, "invert", 0) != 0 {
		value = 127 - value
	}
	return value * maxOut / 127
}

// dispatch executes the action for an event. Must be called with e.mu held.
func (e *Executor) dispatch(ev Event) {
	value := ev.Value
	switch ev.Action {
	case "ptt":
		// Synchronous under mu, so press and release reach the radio in order.
		// Never key after Close has released PTT.
		if !e.closed {
			e.pttOn = true
			e.cat.SetPTT(true)
		}

	case "band_up":
		go e.cat.BandUp()

	case "band_down":
		go e.cat.BandDown()

	case "vfo_a":
		go e.cat.SelectVFO(0)

	case "vfo_b":
		go e.cat.SelectVFO(1)

	case "vfo_swap":
		go e.cat.SwapVFO()

	case "vfo_a_to_b":
		go e.cat.VFOAtoB()

	case "vfo_b_to_a":
		go e.cat.VFOBtoA()

	case "volume":
		level := ccValue(value, 60, ev.Opts)
		go e.volumeTarget().SetVolume(0, level)

	case "volume_b":
		level := ccValue(value, 60, ev.Opts)
		go e.volumeTarget().SetVolume(1, level)

	case "rf_gain":
		level := ccValue(value, 60, ev.Opts)
		go e.cat.SetRFGain(0, level)

	case "rf_gain_b":
		level := ccValue(value, 60, ev.Opts)
		go e.cat.SetRFGain(1, level)

	case "power":
		v := value
		if optFloat(ev.Opts, "invert", 0) != 0 {
			v = 127 - v
		}
		// CC 0-100: 0.1W to 10.0W (0.1W per step, QRP range)
		// CC 101-127: ~11W to 110W (~3.7W per step, QRO range)
		if v <= 100 {
			tenths := v + 1 // 1-101 -> 0.1W-10.1W, clamped to 100 max
			if tenths > 100 {
				tenths = 100
			}
			go e.cat.SetPower(tenths)
		} else {
			watts := 11 + (v-101)*99/26 // 11W-110W
			go e.cat.SetPower(watts * 10)
		}

	case "speed":
		wpm := 5 + value*94/127
		go e.cat.SetSpeed(wpm)

	case "rit":
		e.ritOn = !e.ritOn
		go e.cat.SetRIT(e.ritOn)

	case "xit":
		e.xitOn = !e.xitOn
		go e.cat.SetXIT(e.xitOn)

	case "rit_clear":
		e.ritOn = false
		go e.cat.ClearRIT()

	case "nb":
		e.nbOn = !e.nbOn
		go e.cat.SetNB(e.nbOn)

	case "nr":
		e.nrOn = !e.nrOn
		go e.cat.SetNR(e.nrOn)

	case "split":
		e.splitOn = !e.splitOn
		go e.cat.SetSplit(e.splitOn)

	case "vfo_tune":
		step := int(optFloat(ev.Opts, "step", 10))
		e.tuneVFO(value, step)

	case "vfo_b_tune":
		step := int(optFloat(ev.Opts, "step", 10))
		e.tuneVFOB(value, step)

	case "filter_bw":
		minHz := int(optFloat(ev.Opts, "min", 50))
		maxHz := int(optFloat(ev.Opts, "max", 3000))
		bw := minHz + value*(maxHz-minHz)/127
		go e.cat.SetFilterBW(bw)

	case "filter_adjust":
		step := int(optFloat(ev.Opts, "step", 50))
		if delta := relativeCC(value, step); delta != 0 {
			e.adjustFilter(delta)
		}

	case "filter_up":
		step := int(optFloat(ev.Opts, "step", 50))
		e.adjustFilter(step)

	case "filter_down":
		step := int(optFloat(ev.Opts, "step", 50))
		e.adjustFilter(-step)

	case "notch":
		e.notchOn = !e.notchOn
		go e.cat.SetNotch(e.notchOn)

	case "notch_freq":
		minHz := int(optFloat(ev.Opts, "min", 150))
		maxHz := int(optFloat(ev.Opts, "max", 3000))
		hz := minHz + value*(maxHz-minHz)/127
		e.notchOn = true
		e.notchHz = hz
		go e.cat.SetNotchFreq(hz)

	case "rf_gain_adjust":
		step := int(optFloat(ev.Opts, "step", 1))
		if delta := relativeCC(value, step); delta != 0 {
			e.adjustRFGain(0, delta)
		}

	case "rf_gain_b_adjust":
		step := int(optFloat(ev.Opts, "step", 1))
		if delta := relativeCC(value, step); delta != 0 {
			e.adjustRFGain(1, delta)
		}

	case "rf_gain_up":
		step := int(optFloat(ev.Opts, "step", 1))
		e.adjustRFGain(0, -step)

	case "rf_gain_down":
		step := int(optFloat(ev.Opts, "step", 1))
		e.adjustRFGain(0, step)

	case "rf_gain_b_up":
		step := int(optFloat(ev.Opts, "step", 1))
		e.adjustRFGain(1, -step)

	case "rf_gain_b_down":
		step := int(optFloat(ev.Opts, "step", 1))
		e.adjustRFGain(1, step)

	case "rf_gain_toggle":
		go e.cat.ToggleRFGain(0)

	case "rf_gain_b_toggle":
		go e.cat.ToggleRFGain(1)

	case "cw_msg":
		if s, ok := ev.Opts["text"].(string); ok && s != "" {
			go e.cat.SendCWMessage(s)
		}

	case "dit_paddle":
		go e.cat.SendCWElement(!e.paddleSwap)

	case "dah_paddle":
		go e.cat.SendCWElement(e.paddleSwap)

	default:
		log.Printf("controls: unknown action %q", ev.Action)
	}
}

// relativeCC decodes a 2's complement relative CC value.
// 1-63 = positive (CW), 65-127 = negative (CCW), 0/64 = no movement.
func relativeCC(value, step int) int {
	if value >= 1 && value <= 63 {
		return value * step
	} else if value >= 65 && value <= 127 {
		return (value - 128) * step
	}
	return 0
}

// tuneVFO adjusts VFO A frequency relative to current. It caches the frequency
// and throttles CAT commands to ~20/sec, and re-queries the radio on the first
// step after a 250 ms gap so band changes and external tuning are picked up.
// Must be called with e.mu held.
func (e *Executor) tuneVFO(value, step int) {
	delta := relativeCC(value, step)
	if delta == 0 {
		return
	}

	now := time.Now()

	// Re-query radio on first turn after idle (frequency may have changed externally)
	if !e.vfoKnown || now.Sub(e.lastTuneAt) > 250*time.Millisecond {
		hz, err := e.cat.GetFreqA()
		if err != nil {
			log.Printf("controls: GetFreqA failed: %v", err)
			return
		}
		e.vfoFreq = hz
		e.vfoKnown = true
	}

	e.vfoFreq += delta
	if e.vfoFreq < 0 {
		e.vfoFreq = 0
	}

	// Throttle: max ~20 commands/sec to the radio
	if now.Sub(e.lastTuneAt) < 50*time.Millisecond {
		return
	}
	e.lastTuneAt = now
	e.cat.SetFreqA(e.vfoFreq)
}

// tuneVFOB adjusts VFO B frequency relative to current, with the same caching
// and throttling as tuneVFO. Must be called with e.mu held.
func (e *Executor) tuneVFOB(value, step int) {
	delta := relativeCC(value, step)
	if delta == 0 {
		return
	}

	now := time.Now()

	if !e.vfoBKnown || now.Sub(e.lastTuneBAt) > 250*time.Millisecond {
		hz, err := e.cat.GetFreqB()
		if err != nil {
			log.Printf("controls: GetFreqB failed: %v", err)
			return
		}
		e.vfoBFreq = hz
		e.vfoBKnown = true
	}

	e.vfoBFreq += delta
	if e.vfoBFreq < 0 {
		e.vfoBFreq = 0
	}

	if now.Sub(e.lastTuneBAt) < 50*time.Millisecond {
		return
	}
	e.lastTuneBAt = now
	e.cat.SetFreqB(e.vfoBFreq)
}

// adjustRFGain changes the RF gain attenuation by delta dB. Tracks the level
// locally (defaults to 0 = full gain). Must be called with e.mu held.
func (e *Executor) adjustRFGain(recv int, delta int) {
	gain := &e.rfGain
	if recv == 1 {
		gain = &e.rfGainB
	}
	*gain += delta
	if *gain < 0 {
		*gain = 0
	}
	if *gain > 60 {
		*gain = 60
	}
	go e.cat.SetRFGain(recv, *gain)
}

// adjustFilter changes the filter bandwidth by deltaHz. Tracks the width
// locally (defaults to 500 Hz if unknown). Must be called with e.mu held.
func (e *Executor) adjustFilter(deltaHz int) {
	if e.filterBW == 0 {
		e.filterBW = 500 // sensible CW default
	}
	e.filterBW += deltaHz
	if e.filterBW < 50 {
		e.filterBW = 50
	}
	if e.filterBW > 4000 {
		e.filterBW = 4000
	}
	go e.cat.SetFilterBW(e.filterBW)
}
