package cat

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestK3(t *testing.T) (*K3Radio, *fakeK3) {
	t.Helper()
	f := newFakeK3(t)
	c := NewCATClient()
	if err := c.ConnectTCP(f.addr()); err != nil {
		t.Fatal(err)
	}
	k, err := NewK3Radio(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Close)
	return k, f
}

// after runs fn and returns the commands the fake received because of it,
// using an FB query as a fence (the fake handles commands in order).
func after(t *testing.T, k *K3Radio, f *fakeK3, fn func()) []string {
	t.Helper()
	n := len(f.commands())
	fn()
	if _, err := k.GetFreqB(); err != nil {
		t.Fatalf("fence query: %v", err)
	}
	got := f.commands()[n:]
	return got[:len(got)-1] // drop the fence
}

func TestK3RadioInit(t *testing.T) {
	_, f := newTestK3(t)
	got := f.commands()[:4]
	if want := []string{"K20", "K31", "AI0", "ID"}; !slices.Equal(got, want) {
		t.Fatalf("init commands = %v, want %v", got, want)
	}
}

func TestK3RadioStartsWithK3Off(t *testing.T) {
	f := newFakeK3(t)
	f.set(func(f *fakeK3) { f.silent = true })
	c := NewCATClient()
	if err := c.ConnectTCP(f.addr()); err != nil {
		t.Fatal(err)
	}
	k, err := NewK3Radio(c)
	if err != nil {
		t.Fatalf("NewK3Radio with the K3 off: %v", err)
	}
	t.Cleanup(k.Close)
	f.set(func(f *fakeK3) { f.silent = false; f.cmds = nil })
	if hz, err := k.GetFreqA(); err != nil || hz != 14083999 {
		t.Fatalf("GetFreqA = %d, %v", hz, err)
	}
	if got := f.commands()[:5]; !slices.Equal(got, []string{"K3", "K20", "K31", "AI0", "FA"}) {
		t.Fatalf("commands after power-on = %v, want K3 K20 K31 AI0 FA", got)
	}
}

func TestK3RadioTranslations(t *testing.T) {
	k, f := newTestK3(t)
	tests := []struct {
		name string
		call func() error
		want []string
	}{
		{"speed clamps high", func() error { return k.SetSpeed(60) }, []string{"KS050"}},
		{"speed clamps low", func() error { return k.SetSpeed(5) }, []string{"KS008"}},
		{"speed", func() error { return k.SetSpeed(26) }, []string{"KS026"}},
		{"AF gain", func() error { return k.SetVolume(0, 30) }, []string{"AG128"}},
		{"sub AF gain max", func() error { return k.SetVolume(1, 60) }, []string{"AG$255"}},
		{"RF gain", func() error { return k.SetRFGain(0, 10) }, []string{"RG240"}},
		{"sub RF gain", func() error { return k.SetRFGain(1, 60) }, []string{"RG$190"}},
		{"power high", func() error { return k.SetPower(500) }, []string{"PC050"}},
		{"power max", func() error { return k.SetPower(1100) }, []string{"PC110"}},
		{"power QRP rounds", func() error { return k.SetPower(55) }, []string{"PC006"}},
		{"band up", k.BandUp, []string{"SWT10"}},
		{"band down", k.BandDown, []string{"SWT09"}},
		{"swap", k.SwapVFO, []string{"SWT11"}},
		{"freq A", func() error { return k.SetFreqA(14025000) }, []string{"FA00014025000"}},
		{"CW", k.SetCWMode, []string{"MD3"}},
		{"data", k.SetDataMode, []string{"MD6", "DT2"}},
		{"split", func() error { return k.SetSplit(true) }, []string{"FT1"}},
		{"RIT", func() error { return k.SetRIT(true) }, []string{"RT1"}},
		{"XIT", func() error { return k.SetXIT(false) }, []string{"XT0"}},
		{"NB", func() error { return k.SetNB(true) }, []string{"NB1"}},
		{"filter", func() error { return k.SetFilterBW(2400) }, []string{"BW0240"}},
		{"PTT on", func() error { return k.SetPTT(true) }, []string{"TX"}},
		{"PTT off", func() error { return k.SetPTT(false) }, []string{"RX"}},
		{"abort", k.SendAbort, []string{"RX"}},
		{"RTTY EOT is a no-op", k.SendRTTYEOT, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			got := after(t, k, f, func() { err = tt.call() })
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("sent %v, want %v", got, tt.want)
			}
		})
	}
}

func TestK3RadioToggleRFGain(t *testing.T) {
	k, f := newTestK3(t)
	got := after(t, k, f, func() {
		k.SetRFGain(0, 20)
		k.ToggleRFGain(0)
		k.ToggleRFGain(0)
	})
	if want := []string{"RG230", "RG250", "RG230"}; !slices.Equal(got, want) {
		t.Fatalf("sent %v, want %v", got, want)
	}
}

func TestK3RadioUnsupported(t *testing.T) {
	k, f := newTestK3(t)
	calls := map[string]func() error{
		"SetNotchFreq":  func() error { return k.SetNotchFreq(1000) },
		"SetTextDecode": func() error { return k.SetTextDecode(true) },
		"QueryDecode":   func() error { _, err := k.QueryDecode(); return err },
		"SendCWElement": func() error { return k.SendCWElement(true) },
	}
	for name, call := range calls {
		var err error
		got := after(t, k, f, func() { err = call() })
		if !errors.Is(err, ErrUnsupported) || len(got) != 0 {
			t.Errorf("%s: err = %v, sent %v; want ErrUnsupported and nothing sent", name, err, got)
		}
	}
}

func TestK3RadioQueries(t *testing.T) {
	k, _ := newTestK3(t)
	if hz, err := k.GetFreqA(); err != nil || hz != 14083999 {
		t.Errorf("GetFreqA = %d, %v", hz, err)
	}
	if hz, err := k.GetFreqB(); err != nil || hz != 7042369 {
		t.Errorf("GetFreqB = %d, %v", hz, err)
	}
	if m, err := k.QueryMode(); err != nil || m != 3 {
		t.Errorf("QueryMode = %d, %v", m, err)
	}
	if w, err := k.QuerySpeed(); err != nil || w != 26 {
		t.Errorf("QuerySpeed = %d, %v", w, err)
	}
}

// A timeout followed by a K31 reply: the K3 was only slow. Nothing is
// re-sent and nothing is reset.
func TestK3RadioSlowReplyRecoversWithoutReset(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) { f.silent = true })
	if _, err := k.GetFreqA(); !errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("err = %v, want ErrQueryTimeout", err)
	}
	f.set(func(f *fakeK3) { f.silent = false; f.cmds = nil })
	if _, err := k.GetFreqA(); err != nil {
		t.Fatal(err)
	}
	if got := f.commands(); !slices.Equal(got, []string{"K3", "FA"}) {
		t.Fatalf("recovery commands = %v, want [K3 FA]", got)
	}
	f.set(func(f *fakeK3) { f.cmds = nil })
	k.GetFreqA()
	if got := f.commands(); !slices.Equal(got, []string{"FA"}) {
		t.Fatalf("second query sent %v, want just FA", got)
	}
}

// A timeout followed by a K30 reply: the K3 was power cycled, so the modes
// are restored before the query.
func TestK3RadioPowerCycleRestoresModes(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) { f.silent = true })
	k.GetFreqA()
	f.set(func(f *fakeK3) { f.silent = false; f.k3mode = '0'; f.cmds = nil })
	if _, err := k.GetFreqA(); err != nil {
		t.Fatal(err)
	}
	if got := f.commands(); !slices.Equal(got, []string{"K3", "K20", "K31", "AI0", "FA"}) {
		t.Fatalf("recovery commands = %v, want [K3 K20 K31 AI0 FA]", got)
	}
}

// Query B starts while query A is outstanding; A then times out and the K3
// comes back reset. B must run the recovery, not act on the "no recovery
// needed" it would have seen when it started.
func TestK3RadioConcurrentQueryRecovers(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) { f.silent = true })
	aDone := make(chan struct{})
	go func() { k.GetFreqA(); close(aDone) }()
	time.Sleep(50 * time.Millisecond) // A's FA is outstanding
	bErr := make(chan error, 1)
	go func() { _, err := k.GetFreqB(); bErr <- err }()
	time.Sleep(300 * time.Millisecond)
	// Power cycled before A times out; B hasn't sent anything yet.
	f.set(func(f *fakeK3) { f.silent = false; f.k3mode = '0'; f.cmds = nil })
	<-aDone
	if err := <-bErr; err != nil {
		t.Fatal(err)
	}
	if got := f.commands(); !slices.Equal(got, []string{"K3", "K20", "K31", "AI0", "FB"}) {
		t.Fatalf("B sent %v, want [K3 K20 K31 AI0 FB]", got)
	}
}

// A slow reply while an NR tap is in flight must not clear it: a second tap
// would undo the first when both land.
func TestK3RadioSlowReplyKeepsPendingTap(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) { f.tapDelay = time.Second })
	k.SetNR(true)
	waitUntil(t, "first tap", func() bool { return f.count("SWT34") == 1 })
	f.set(func(f *fakeK3) { f.silent = true })
	waitUntil(t, "a DS read to time out", func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		return k.reinit
	})
	f.set(func(f *fakeK3) { f.silent = false })
	waitUntil(t, "idle", k.tg.idle)
	f.mu.Lock()
	nr := f.nr
	f.mu.Unlock()
	if n := f.count("SWT34"); n != 1 || !nr {
		t.Fatalf("SWT34 sent %d times, NR = %v; want 1 tap, NR on", n, nr)
	}
}

func TestK3RadioKYOneCharPerWrite(t *testing.T) {
	k, f := newTestK3(t)
	if err := k.SendKY("CQ TEST"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "text sent", func() bool { return f.sentText() == "CQ TEST" })
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "KY ") && len(c) != 4 {
			t.Fatalf("KY write %q carries more than one character", c)
		}
	}
}

func TestK3RadioRTTYThroughFiniteBuffer(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) { f.capacity = 8 })
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
				f.drain(1)
			}
		}
	}()
	msg := "CQ TEST NZ1U NZ1U TEST CQ RYRY"
	if err := k.SendRTTYText(msg); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "RTTY sent", func() bool { return f.sentText() == msg })
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.overflow != 0 {
		t.Fatalf("overflow = %d", f.overflow)
	}
}

func TestK3RadioNRReadsStateFirst(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) { f.nr = true }) // NR already on, as after a restart
	k.SetNR(true)
	waitUntil(t, "idle", k.tg.idle)
	if n := f.count("SWT34"); n != 0 {
		t.Fatalf("SWT34 sent %d times with NR already on", n)
	}
	k.SetNR(false)
	waitUntil(t, "idle", k.tg.idle)
	if n := f.count("SWT34"); n != 1 {
		t.Fatalf("SWT34 sent %d times, want 1", n)
	}
	k.SetNotch(true)
	waitUntil(t, "idle", k.tg.idle)
	if n := f.count("SWT32"); n != 1 {
		t.Fatalf("SWT32 sent %d times, want 1", n)
	}
}

func TestK3RadioDSWithSemicolonTapsNothing(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) { f.nr = true; f.dsText = "14;83999" })
	k.SetNR(false)
	time.Sleep(100 * time.Millisecond)
	if n := f.count("SWT34"); n != 0 {
		t.Fatalf("SWT34 sent %d times from a malformed DS reply", n)
	}
}

// TestK3RadioNRDoesNotDelayRX: an NR press then Esc on the same RadioServer
// connection; RX must not wait for the reconciler's DS read.
func TestK3RadioNRDoesNotDelayRX(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) { f.dsDelay = 400 * time.Millisecond })
	srv, err := NewRadioServer(k, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Run()
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv.Addr())
	defer c.close()
	start := time.Now()
	c.send(t, "NR1;RX;")
	waitUntil(t, "RX at the K3", func() bool { return f.count("RX") >= 1 })
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Fatalf("RX reached the K3 after %v", d)
	}
}

func TestK3RadioRawPolicy(t *testing.T) {
	k, f := newTestK3(t)

	if got, err := k.Raw("IF"); err != nil || got != "IF00014083999     -000000 0003000001 " {
		t.Errorf("Raw(IF) = %q, %v", got, err)
	}
	if got, err := k.Raw("SB"); err != nil || got != "SB0" {
		t.Errorf("Raw(SB) = %q, %v", got, err)
	}
	if _, err := k.Raw("XX"); !errors.Is(err, ErrRadioRejected) {
		t.Errorf("Raw(XX) err = %v, want ErrRadioRejected", err)
	}

	sent := after(t, k, f, func() {
		if got, err := k.Raw("K41"); err != nil || got != "K41" {
			t.Errorf("Raw(K41) = %q, %v", got, err)
		}
		if got, err := k.Raw("K4"); err != nil || got != "K41" {
			t.Errorf("Raw(K4) = %q, %v", got, err)
		}
		if got, err := k.Raw("K2"); err != nil || got != "K20" {
			t.Errorf("Raw(K2) = %q, %v", got, err)
		}
		if got, err := k.Raw("K3"); err != nil || got != "K31" {
			t.Errorf("Raw(K3) = %q, %v", got, err)
		}
		for _, cmd := range []string{"K20", "K31"} {
			if got, err := k.Raw(cmd); err != nil || got != "" {
				t.Errorf("Raw(%s) = %q, %v; want accepted as a no-op", cmd, got, err)
			}
		}
		for _, cmd := range []string{"K22", "K21", "K23", "K30", "KYW TEST", "SWH34", "SWH32"} {
			if _, err := k.Raw(cmd); !errors.Is(err, ErrUnsupported) {
				t.Errorf("Raw(%s) err = %v, want ErrUnsupported", cmd, err)
			}
		}
	})
	if len(sent) != 0 {
		t.Errorf("answered/rejected commands reached the K3: %v", sent)
	}

	sent = after(t, k, f, func() {
		if got, err := k.Raw("AI2"); err != nil || got != "AI0" {
			t.Errorf("Raw(AI2) = %q, %v", got, err)
		}
		if got, err := k.Raw("MD2"); err != nil || got != "" {
			t.Errorf("Raw(MD2) = %q, %v", got, err)
		}
	})
	if !slices.Equal(sent, []string{"AI0", "MD2"}) {
		t.Errorf("sent %v, want [AI0 MD2]", sent)
	}

	k.Raw("KY HI")
	waitUntil(t, "raw KY queued", func() bool { return f.sentText() == "HI" })

	k.Raw("SWT34") // NR off → on
	waitUntil(t, "idle", k.tg.idle)
	if f.count("SWT34") != 1 {
		t.Errorf("SWT34 count = %d, want 1", f.count("SWT34"))
	}
}

func TestCloseWithinBreaksStalledStep(t *testing.T) {
	unblock := make(chan struct{})
	stalls := 0
	start := time.Now()
	closeWithin(30*time.Millisecond, func() { stalls++; close(unblock) }, func() { <-unblock })
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("closeWithin took %v", d)
	}
	if stalls != 1 {
		t.Fatalf("stall called %d times, want 1", stalls)
	}
}

func TestCloseWithinNoStallWhenPrompt(t *testing.T) {
	closeWithin(time.Second, func() { t.Error("stall called for prompt steps") }, func() {}, func() {})
}

// TestK3RadioCloseWithStuckKYWrite: a KY write stuck in the serial driver
// holds sendMu, so Abort can't send RX. Closing the link must break it.
func TestK3RadioCloseWithStuckKYWrite(t *testing.T) {
	unblock := make(chan struct{})
	writing := make(chan struct{})
	b := &kyBuf{capacity: 32}
	s := newKYSender(b.room, func(string) error {
		close(writing)
		<-unblock
		return errors.New("port closed")
	}, b.abort)
	s.start()
	s.Enqueue("A")
	<-writing
	done := make(chan struct{})
	go func() {
		closeWithin(30*time.Millisecond, func() { close(unblock) }, s.Close)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown hung behind a stuck KY write")
	}
}

func TestK3RadioReverseTranslationOfFrontPanelValues(t *testing.T) {
	k, f := newTestK3(t)
	f.set(func(f *fakeK3) {
		f.vals["AG"] = "AG130" // between two K4 steps
		f.vals["RG"] = "RG100" // below the K4's −60 dB floor
		f.vals["PC"] = "PC007"
	})
	for q, want := range map[string]string{"AG": "AG031", "RG": "RG-60", "PC": "PC007H"} {
		if got, err := k.Raw(q); err != nil || got != want {
			t.Errorf("Raw(%s) = %q, %v; want %q", q, got, err, want)
		}
	}
}

// TestK3RadioRoundTripThroughRadioServer: every K4 value set through
// RadioServer reads back unchanged in K4 form.
func TestK3RadioRoundTripThroughRadioServer(t *testing.T) {
	k, _ := newTestK3(t)
	srv, err := NewRadioServer(k, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Run()
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	check := func(set, query, want string) {
		t.Helper()
		c.send(t, set+";"+query+";")
		if got := c.recv(t); got != want {
			t.Errorf("%s then %s: got %q, want %q", set, query, got, want)
		}
	}
	for l := 0; l <= 60; l++ {
		check(fmt.Sprintf("AG%03d", l), "AG", fmt.Sprintf("AG%03d", l))
		check(fmt.Sprintf("RG-%02d", l), "RG", fmt.Sprintf("RG-%02d", l))
	}
	for _, l := range []int{0, 17, 60} {
		check(fmt.Sprintf("AG$%03d", l), "AG$", fmt.Sprintf("AG$%03d", l))
		check(fmt.Sprintf("RG$-%02d", l), "RG$", fmt.Sprintf("RG$-%02d", l))
	}
	for w := 1; w <= 110; w++ {
		check(fmt.Sprintf("PC%03dH", w), "PC", fmt.Sprintf("PC%03dH", w))
	}
}
