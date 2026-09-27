package cat

import (
	"errors"
	"testing"
)

// TestRadioServerUnknownQueryRelaysRawReply: N1MM (set to K4) polls IF, SB
// and DV; RadioServer has no typed handler for them and must relay the
// radio's own reply.
func TestRadioServerUnknownQueryRelaysRawReply(t *testing.T) {
	mock := &mockRadio{raw: map[string]string{
		"IF": "IF00014083999     -000000 0006000001 ",
		"SB": "SB0",
		"DV": "DV0",
	}}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	for _, q := range []string{"IF", "SB", "DV"} {
		c.send(t, q+";")
		if got := c.recv(t); got != mock.raw[q] {
			t.Errorf("%s: got %q, want %q", q, got, mock.raw[q])
		}
	}
}

// TestRadioServerBareQueryOfSetOnlyHandlerGoesToRaw: handlers such as AG and
// FT only implement the set form; their bare query goes to Raw.
func TestRadioServerBareQueryOfSetOnlyHandlerGoesToRaw(t *testing.T) {
	mock := &mockRadio{raw: map[string]string{
		"AG": "AG030", "AG$": "AG$020", "RG": "RG-10", "RG$": "RG$-05",
		"FT": "FT0", "BW": "BW0240", "PC": "PC050H", "NB": "NB0",
		"NR": "NR0", "RT": "RT0", "XT": "XT0",
	}}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	for q, want := range mock.raw {
		c.send(t, q+";")
		if got := c.recv(t); got != want {
			t.Errorf("%s: got %q, want %q", q, got, want)
		}
	}
}

// TestRadioServerUnhandledFormsGoToRaw: forms a typed handler doesn't
// implement used to be dropped silently (MD only acted on MD3 and MD6).
func TestRadioServerUnhandledFormsGoToRaw(t *testing.T) {
	mock := &mockRadio{freqA: 14060000}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	for _, cmd := range []string{
		"MD1", "MD2", "MD4", "MD5", "MD7", "MD9", "MD$3", "DT1",
		"SWT09", "SWH18", "KYW", "NM$1", "KZX", "PC050", "ZZ",
		// Sub-receiver queries must never reach a setter: NB$ used to call
		// SetNB(false), and NM$ SetNotch(false).
		"NB$", "NR$", "RT$", "XT$", "NM$", "TD$", "FT$", "KS$", "BW$",
		"PC$", "MD$", "NB2", "RT9", "TD$/",
	} {
		mock.reset()
		c.send(t, cmd+";")
		c.send(t, "FA;")
		c.recv(t)
		got := mock.getCalls()
		if len(got) != 2 || got[0] != "Raw("+cmd+")" || got[1] != "GetFreqA()" {
			t.Errorf("%s: calls = %v, want [Raw(%s) GetFreqA()]", cmd, got, cmd)
		}
	}
}

// TestRadioServerRawErrorWritesNothing: a Raw error (timeout, "?;") is a
// missed poll for the client, never data.
func TestRadioServerRawErrorWritesNothing(t *testing.T) {
	mock := &mockRadio{freqA: 14060000, rawErr: errors.New("IF: query timeout")}
	srv := setupRadioServer(t, mock)
	c := newTestClient(t, srv.Addr())
	defer c.close()

	c.send(t, "IF;")
	c.send(t, "FA;")
	if got := c.recv(t); got != "FA00014060000" {
		t.Fatalf("got %q, want FA00014060000 (nothing written for the failed IF)", got)
	}
}
