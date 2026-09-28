package cat

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeK3 is an in-memory Elecraft K3 on a TCP listener, for K3Radio tests.
// It answers bare queries from vals, remembers sets as the reply to the
// matching query, keeps a finite KY text buffer, and holds the NR/NTCH
// switch state that SWT34/SWT32 flip and DS reports in its last byte (in K31
// mode only, as on the radio).
type fakeK3 struct {
	ln net.Listener

	mu       sync.Mutex
	conn     net.Conn
	cmds     []string          // every command received, in order
	vals     map[string]string // replies to bare queries
	silent   bool              // powered off: no replies
	capacity int               // KY buffer size; 0 = unlimited
	buffered int
	sent     []byte // characters accepted into the KY buffer
	overflow int
	nr, ntch bool
	tapDelay time.Duration // tap-to-effect delay
	dsText   string        // the 8 display bytes in a DS reply
	dsDelay  time.Duration // DS reply delay (written from another goroutine)
	k3mode   byte          // '0' after power-up, '1' once K31 is set
	tx       bool          // transmitting (TX until RX)
}

func newFakeK3(t *testing.T) *fakeK3 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeK3{ln: ln, dsText: "14083999", k3mode: '0', vals: map[string]string{
		"ID": "ID017", "FA": "FA00014083999", "FB": "FB00007042369",
		"MD": "MD3", "KS": "KS026", "AG": "AG015", "AG$": "AG$015",
		"RG": "RG250", "RG$": "RG$250", "PC": "PC050", "BW": "BW0240",
		"IF": "IF00014083999     -000000 0003000001 ", "SB": "SB0", "DV": "DV0",
	}}
	go f.serve()
	t.Cleanup(func() {
		ln.Close()
		f.mu.Lock()
		if f.conn != nil {
			f.conn.Close()
		}
		f.mu.Unlock()
	})
	return f
}

func (f *fakeK3) addr() string { return f.ln.Addr().String() }

func (f *fakeK3) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	var wmu sync.Mutex
	reply := func(s string) {
		wmu.Lock()
		defer wmu.Unlock()
		fmt.Fprint(conn, s+";")
	}
	sc := bufio.NewScanner(conn)
	sc.Split(SplitSemicolon)
	for sc.Scan() {
		cmd := sc.Text()
		if cmd == "" {
			continue
		}
		r, delay := f.handle(cmd)
		switch {
		case r == "":
		case delay > 0:
			go func() { time.Sleep(delay); reply(r) }()
		default:
			reply(r)
		}
	}
}

// handle applies one command; it returns the reply without ';' ("" = none)
// and how long to hold it.
func (f *fakeK3) handle(cmd string) (string, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	if f.silent {
		return "", 0
	}
	switch {
	case cmd == "K3":
		return "K3" + string(f.k3mode), 0
	case cmd == "K30" || cmd == "K31":
		f.k3mode = cmd[2]
		return "", 0
	case cmd == "TX":
		f.tx = true
		return "", 0
	case cmd == "RX":
		// RX ends transmit; the fake also discards queued text. If Task 0
		// found that RX keeps buffered text, drop this line.
		f.tx = false
		f.buffered = 0
		return "", 0
	case cmd == "KY":
		if f.capacity > 0 && f.buffered >= f.capacity {
			return "KY1", 0
		}
		return "KY0", 0
	case strings.HasPrefix(cmd, "KY "):
		for i := 3; i < len(cmd); i++ {
			if f.capacity > 0 && f.buffered >= f.capacity {
				f.overflow++
				continue
			}
			f.buffered++
			f.sent = append(f.sent, cmd[i])
		}
		return "", 0
	case cmd == "DS":
		// Byte f carries the NR/NTCH icons only in K31 mode; in K30 (after
		// power-up) it reads as all clear.
		b := byte(0x80)
		if f.nr && f.k3mode == '1' {
			b |= 0x04
		}
		if f.ntch && f.k3mode == '1' {
			b |= 0x02
		}
		return "DS" + f.dsText + "\x80" + string([]byte{b}), f.dsDelay
	case cmd == "SWT34" || cmd == "SWT32":
		isNR := cmd == "SWT34"
		flip := func() {
			if isNR {
				f.nr = !f.nr
			} else {
				f.ntch = !f.ntch
			}
		}
		if f.tapDelay > 0 {
			time.AfterFunc(f.tapDelay, func() { f.mu.Lock(); flip(); f.mu.Unlock() })
		} else {
			flip()
		}
		return "", 0
	case isBareQuery(cmd):
		if v, ok := f.vals[cmd]; ok {
			return v, 0
		}
		return "?", 0
	}
	for _, key := range []string{"AG$", "RG$", "MD$", "AG", "RG", "PC", "KS", "FA", "FB", "MD", "BW"} {
		if strings.HasPrefix(cmd, key) && len(cmd) > len(key) {
			f.vals[key] = cmd
			break
		}
	}
	return "", 0
}

func (f *fakeK3) set(fn func(f *fakeK3)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeK3) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

func (f *fakeK3) count(cmd string) int {
	n := 0
	for _, c := range f.commands() {
		if c == cmd {
			n++
		}
	}
	return n
}

func (f *fakeK3) sentText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.sent)
}

func (f *fakeK3) drain(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buffered = max(0, f.buffered-n)
}
