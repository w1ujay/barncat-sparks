package cat

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnsupported is returned by radio methods the connected model can't do.
var ErrUnsupported = errors.New("not supported on this radio")

// errK3Closed is returned by methods that would key the radio after Close.
var errK3Closed = errors.New("K3: radio closed")

// k3Init pins the K3's command modes: K2 normal (basic KY0/KY1 replies), K3
// extended (DS icon byte f), and no auto-info.
const k3Init = "K20;K31;AI0;"

// K3Radio implements Radio for an Elecraft K3 on a serial CATClient. Typed
// methods translate Sparks's K4 dialect; Raw applies the K3 policy table for
// commands RadioServer doesn't model (see the remote K3 design). KY text goes
// through a paced sender, and NR/notch through an asynchronous reconciler.
type K3Radio struct {
	c  *CATClient
	ky *kySender
	tg *toggler

	closeTimeout time.Duration // how long Close waits before breaking stuck I/O

	// qmu serializes query as one operation: the recovery decision, any
	// recovery commands, the query itself and the reinit update. Lock order:
	// qmu → CATClient.queryMu → CATClient.writeMu. Abort never takes it.
	qmu sync.Mutex

	mu      sync.Mutex // guards the fields below; never held across CAT I/O
	reinit  bool       // a query timed out: check for a reset before the next query
	closed  bool       // Close has begun: nothing may key the radio
	rfSaved [2]int     // last RF gain (K3 units) set per receiver
	rfAtMax [2]bool    // ToggleRFGain moved that receiver to 250
	warned  map[string]bool
}

// NewK3Radio wraps c, unkeys the K3 (a transmitter left keyed by a crash or
// link loss before this restart), pins its command modes and checks it is a
// K3. A K3 that doesn't answer (powered off) is not an error: the modes are
// restored before the first query once it does.
func NewK3Radio(c *CATClient) (*K3Radio, error) {
	k := &K3Radio{c: c, closeTimeout: 2 * time.Second, rfSaved: [2]int{250, 250}, warned: map[string]bool{}}
	// After a query ends without its reply (timeout, or rejection grace),
	// resync with ID so that late reply can't answer the next query (a KY
	// poll's late KY0 would otherwise authorize a write into a full buffer).
	c.setSyncQuery("ID")
	if err := c.Send("RX;" + k3Init); err != nil {
		return nil, fmt.Errorf("K3 init: %w", err)
	}
	switch id, err := c.Raw("ID"); {
	case err != nil:
		log.Printf("K3: no answer to ID (%v); will re-initialize when it answers", err)
		k.reinit = true
	case id != "ID017":
		log.Printf("K3: radio answered %s, expected ID017 (K3)", id)
	}
	k.ky = newKYSender(k.kyRoom, k.kyWrite, k.abortWrite)
	k.tg = newToggler(k.readToggles, k.tapToggle)
	k.ky.start()
	k.tg.start()
	return k, nil
}

var _ Radio = (*K3Radio)(nil)

// Close aborts any keying (sending RX), stops the workers and closes the
// link. A worker stuck in a serial write holds its lock, so a watchdog closes
// the link after closeTimeout: that fails the write and lets shutdown finish.
// Close first marks the radio closed, so no TX can follow the RX.
func (k *K3Radio) Close() {
	k.mu.Lock()
	k.closed = true
	k.mu.Unlock()
	closeWithin(k.closeTimeout, k.c.Close, k.ky.Close, k.tg.Close)
	k.c.Close()
}

// closeWithin runs steps in order. If they haven't all returned after
// timeout, it calls stall once (to break stuck I/O) and keeps waiting.
func closeWithin(timeout time.Duration, stall func(), steps ...func()) {
	t := time.AfterFunc(timeout, func() {
		log.Printf("K3: shutdown stalled for %v; closing the radio link", timeout)
		stall()
	})
	defer t.Stop()
	for _, step := range steps {
		step()
	}
}

// query runs one bare query. After a timeout it first checks whether the K3
// was reset, so a reply (a DS icon byte in particular) is never read with the
// modes lost to a power cycle.
func (k *K3Radio) query(cmd string) (string, error) {
	k.qmu.Lock()
	defer k.qmu.Unlock()
	k.mu.Lock()
	recovering := k.reinit
	k.mu.Unlock()
	reset := false
	if recovering {
		var err error
		if reset, err = k.recover(); err != nil {
			return "", err
		}
	}
	reply, err := k.c.Raw(cmd)
	k.mu.Lock()
	switch {
	case errors.Is(err, ErrQueryTimeout):
		k.reinit = true
	case err == nil && recovering:
		k.reinit = false
	}
	k.mu.Unlock()
	if err == nil && reset {
		k.tg.ForgetPending()
	}
	return reply, err
}

// noteTimeout arms re-init after a query timeout, as query does.
func (k *K3Radio) noteTimeout(err error) {
	if errors.Is(err, ErrQueryTimeout) {
		k.mu.Lock()
		k.reinit = true
		k.mu.Unlock()
	}
}

// recover runs before the first query after a timeout. The K3 powers up in
// K30 and Sparks always sets K31, so a K30 reply proves the radio was reset:
// the modes are restored, and any tap sent before the reset can no longer
// land. A K31 reply means the radio was only slow; nothing is reset.
func (k *K3Radio) recover() (reset bool, err error) {
	mode, err := k.c.rawQuery("K3")
	if err != nil {
		return false, err
	}
	switch mode {
	case "K31":
		return false, nil
	case "K30":
		if err := k.c.Send(k3Init); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("K3: unexpected reply %q to K3", mode)
}

// queryInt runs a bare query and parses the digits after its prefix.
func (k *K3Radio) queryInt(cmd string) (int, error) {
	reply, err := k.query(cmd)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(reply[len(cmd):])
}

func (k *K3Radio) unsupported(what string) error {
	k.mu.Lock()
	first := !k.warned[what]
	k.warned[what] = true
	k.mu.Unlock()
	if first {
		log.Printf("K3: %s is not supported on the K3", what)
	}
	return ErrUnsupported
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

// CW and RTTY text: queued and paced by the KY sender.
func (k *K3Radio) SendKY(text string) error        { return k.ky.Enqueue(text) }
func (k *K3Radio) SendCWMessage(text string) error { return k.ky.Enqueue(text) }
func (k *K3Radio) SendRTTYText(text string) error  { return k.ky.Enqueue(text) }

// SendAbort clears queued text and sends RX, which ends transmit in all K3
// modes, FSK-D included.
func (k *K3Radio) SendAbort() error { return k.ky.Abort() }

// SendRTTYEOT does nothing: the K3's ^D "quickly terminates transmission" and
// would cut queued text; the keyer releases with RX after the air time.
func (k *K3Radio) SendRTTYEOT() error { return nil }

func (k *K3Radio) SendCWElement(bool) error { return k.unsupported("KZ paddle elements") }

// SetPTT(true) sends TX under the KY sender's sendMu, the lock Abort holds
// to send RX. Close marks the radio closed before its RX, so a TX either
// reaches the radio before that RX or is refused: none follows it.
func (k *K3Radio) SetPTT(on bool) error {
	if !on {
		return k.ky.Abort()
	}
	return k.ky.exclusive(func() error {
		k.mu.Lock()
		closed := k.closed
		k.mu.Unlock()
		if closed {
			return errK3Closed
		}
		return k.c.SetPTT(true)
	})
}

func (k *K3Radio) SetSpeed(wpm int) error   { return k.c.SetSpeed(clamp(wpm, 8, 50)) }
func (k *K3Radio) QuerySpeed() (int, error) { return k.queryInt("KS") }

func (k *K3Radio) SetFreqA(hz int) error    { return k.c.SetFreqA(hz) }
func (k *K3Radio) SetFreqB(hz int) error    { return k.c.SetFreqB(hz) }
func (k *K3Radio) GetFreqA() (int, error)   { return k.queryInt("FA") }
func (k *K3Radio) GetFreqB() (int, error)   { return k.queryInt("FB") }
func (k *K3Radio) SelectVFO(ab int) error   { return k.c.SelectVFO(ab) }
func (k *K3Radio) SwapVFO() error           { return k.c.SwapVFO() }
func (k *K3Radio) SetRIT(on bool) error     { return k.c.SetRIT(on) }
func (k *K3Radio) ClearRIT() error          { return k.c.ClearRIT() }
func (k *K3Radio) SetXIT(on bool) error     { return k.c.SetXIT(on) }
func (k *K3Radio) SetNB(on bool) error      { return k.c.SetNB(on) }
func (k *K3Radio) SetSplit(on bool) error   { return k.c.SetSplit(on) }
func (k *K3Radio) SetFilterBW(hz int) error { return k.c.SetFilterBW(hz) }
func (k *K3Radio) SetDataMode() error       { return k.c.SetDataMode() }
func (k *K3Radio) SetCWMode() error         { return k.c.SetCWMode() }
func (k *K3Radio) QueryMode() (int, error)  { return k.queryInt("MD") }

func (k *K3Radio) VFOAtoB() error {
	hz, err := k.GetFreqA()
	if err != nil {
		return err
	}
	return k.SetFreqB(hz)
}

func (k *K3Radio) VFOBtoA() error {
	hz, err := k.GetFreqB()
	if err != nil {
		return err
	}
	return k.SetFreqA(hz)
}

func (k *K3Radio) BandUp() error   { return k.c.Send("SWT10;") } // BAND+ tap
func (k *K3Radio) BandDown() error { return k.c.Send("SWT09;") } // BAND− tap

func sub(recv int) string {
	if recv == 1 {
		return "$"
	}
	return ""
}

// SetVolume scales the K4's 0–60 AF gain to the K3's 0–255.
func (k *K3Radio) SetVolume(recv, level int) error {
	v := (clamp(level, 0, 60)*255 + 30) / 60
	return k.c.Send(fmt.Sprintf("AG%s%03d;", sub(recv), v))
}

// SetRFGain maps the K4's attenuation in dB (0–60) to the K3's 250 (full
// gain) down to 190.
func (k *K3Radio) SetRFGain(recv, level int) error {
	v := 250 - clamp(level, 0, 60)
	k.mu.Lock()
	k.rfSaved[recv&1] = v
	k.rfAtMax[recv&1] = false
	k.mu.Unlock()
	return k.c.Send(fmt.Sprintf("RG%s%03d;", sub(recv), v))
}

// ToggleRFGain alternates between the last level set and full gain; the K3
// has no RG toggle.
func (k *K3Radio) ToggleRFGain(recv int) error {
	k.mu.Lock()
	r := recv & 1
	k.rfAtMax[r] = !k.rfAtMax[r]
	v := k.rfSaved[r]
	if k.rfAtMax[r] {
		v = 250
	}
	k.mu.Unlock()
	return k.c.Send(fmt.Sprintf("RG%s%03d;", sub(recv), v))
}

// SetPower sends whole watts; the K3's 0.1 W unit applies only to
// transverter/TEST output.
func (k *K3Radio) SetPower(tenths int) error {
	return k.c.Send(fmt.Sprintf("PC%03d;", clamp((tenths+5)/10, 0, 110)))
}

func (k *K3Radio) SetNR(on bool) error    { k.tg.Set(filterNR, on); return nil }
func (k *K3Radio) SetNotch(on bool) error { k.tg.Set(filterNotch, on); return nil }

func (k *K3Radio) SetNotchFreq(int) error { return k.unsupported("manual notch frequency") }
func (k *K3Radio) SetTextDecode(bool) error {
	return k.unsupported("text decode")
}
func (k *K3Radio) QueryDecode() (string, error) { return "", k.unsupported("text decode") }

// readToggles reads NR and NTCH from byte f of the DS reply. Byte f carries
// them only in K31 mode, and an idle power cycle (no query timed out, so no
// re-init is armed) leaves the K3 in K30, where they read as off. So each
// read first checks the mode, under qmu as one operation with the DS query:
// K30 restores the modes, drops taps sent before the reset and fails this
// read (the next one reads DS in K31); anything but K31 fails the read.
func (k *K3Radio) readToggles() (bool, bool, error) {
	k.qmu.Lock()
	defer k.qmu.Unlock()
	mode, err := k.c.rawQuery("K3")
	if err != nil {
		k.noteTimeout(err)
		return false, false, err
	}
	switch mode {
	case "K31":
	case "K30":
		err := k.c.Send(k3Init)
		if err == nil {
			k.mu.Lock()
			k.reinit = false // modes restored
			k.mu.Unlock()
		}
		k.tg.ForgetPending()
		if err != nil {
			return false, false, err
		}
		return false, false, errors.New("K3: found in K30 (power cycled); modes restored, DS not read")
	default:
		return false, false, fmt.Errorf("K3: unexpected reply %q to K3", mode)
	}
	// K31: the modes are intact, so the K3 is at most slow; nothing to recover.
	k.mu.Lock()
	k.reinit = false
	k.mu.Unlock()
	reply, err := k.c.Raw("DS")
	if err != nil {
		k.noteTimeout(err)
		return false, false, err
	}
	// "DS" + 8 display bytes + icon byte a + byte f. A display byte can be
	// ';', which splits the reply short: treat that as a failed read.
	if len(reply) != 12 {
		return false, false, fmt.Errorf("K3: malformed DS reply %q", reply)
	}
	f := reply[11]
	return f&0x04 != 0, f&0x02 != 0, nil
}

func (k *K3Radio) tapToggle(f toggleFilter) error {
	if f == filterNR {
		return k.c.Send("SWT34;")
	}
	return k.c.Send("SWT32;")
}

func (k *K3Radio) kyRoom() (bool, error) {
	reply, err := k.query("KY")
	if err != nil {
		return false, err
	}
	switch reply {
	case "KY0":
		return true, nil
	case "KY1":
		return false, nil
	}
	return false, fmt.Errorf("K3: unexpected KY reply %q", reply)
}

func (k *K3Radio) kyWrite(text string) error { return k.c.Send("KY " + text + ";") }
func (k *K3Radio) abortWrite() error         { return k.c.Send("RX;") }

// Raw applies the K3 policy for a command RadioServer doesn't model.
func (k *K3Radio) Raw(cmd string) (string, error) {
	switch {
	case cmd == "KY":
		return k.query(cmd)
	case strings.HasPrefix(cmd, "KY "):
		return "", k.ky.Enqueue(cmd[3:])
	case strings.HasPrefix(cmd, "KY"):
		// Key on the command prefix, not the full client-supplied text: KYW
		// (say) carries arbitrary message text, and logging/recording on the
		// full string would log every distinct message again and grow
		// warned without bound.
		key := cmd
		if len(key) > 3 {
			key = key[:3]
		}
		return "", k.unsupported("KY variant " + key)
	case cmd == "SWT34":
		k.tg.Toggle(filterNR)
		return "", nil
	case cmd == "SWT32":
		k.tg.Toggle(filterNotch)
		return "", nil
	case cmd == "SWH34" || cmd == "SWH32":
		return "", k.unsupported(cmd)
	case cmd == "SWT09" || cmd == "SWT10" || cmd == "SWT11":
		return "", k.c.Send(cmd + ";") // BAND−, BAND+, A/B: verified on the K3
	case isSwitchCode(cmd):
		// Any other switch code may be XMIT (SWT16), TUNE (SWH16) or TEST
		// (SWH18) on the K3: never pass an unverified one through.
		key := cmd
		if len(key) > 5 {
			key = key[:5] + "..."
		}
		return "", k.unsupported("switch code " + key)
	case cmd == "TX":
		return "", k.SetPTT(true)
	case cmd == "K4":
		return "K41", nil
	case len(cmd) == 3 && strings.HasPrefix(cmd, "K4"):
		return cmd, nil // the K3 has no K4 mode; accept and echo
	case cmd == "K2":
		return "K20", nil
	case cmd == "K3":
		return "K31", nil
	case cmd == "K20" || cmd == "K31":
		return "", nil
	case len(cmd) == 3 && (strings.HasPrefix(cmd, "K2") || strings.HasPrefix(cmd, "K3")):
		return "", k.unsupported("meta-mode " + cmd)
	case cmd == "AI":
		return "AI0", nil
	case len(cmd) == 3 && strings.HasPrefix(cmd, "AI"):
		return "AI0", k.c.Send("AI0;")
	case cmd == "AG" || cmd == "AG$":
		return k.translate(cmd, func(v int) string { return fmt.Sprintf("%s%03d", cmd, (v*60+127)/255) })
	case cmd == "RG" || cmd == "RG$":
		return k.translate(cmd, func(v int) string { return fmt.Sprintf("%s-%02d", cmd, clamp(250-v, 0, 60)) })
	case cmd == "PC":
		return k.translate(cmd, func(v int) string { return fmt.Sprintf("PC%03dH", v) })
	case isBareQuery(cmd):
		return k.query(cmd)
	}
	return "", k.c.Send(cmd + ";")
}

// isSwitchCode reports whether cmd is a tap or hold switch emulation:
// "SW", then T or H, then one or more digits. Bare "SW" is the SWR query.
func isSwitchCode(cmd string) bool {
	if len(cmd) < 4 || cmd[:2] != "SW" || (cmd[2] != 'T' && cmd[2] != 'H') {
		return false
	}
	for i := 3; i < len(cmd); i++ {
		if cmd[i] < '0' || cmd[i] > '9' {
			return false
		}
	}
	return true
}

// translate queries cmd in K3 form and rewrites the value into K4 form.
func (k *K3Radio) translate(cmd string, k4 func(v int) string) (string, error) {
	reply, err := k.query(cmd)
	if err != nil {
		return "", err
	}
	v, err := strconv.Atoi(reply[len(cmd):])
	if err != nil {
		return "", fmt.Errorf("K3: bad %s reply %q", cmd, reply)
	}
	return k4(v), nil
}
