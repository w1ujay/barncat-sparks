package keyer

import "testing"

// TestBackspaceRemovesLastUnsentChar: the logger corrects a character with
// backspace before sendLoop has taken it; the removed character must not be
// sent.
func TestBackspaceRemovesLastUnsentChar(t *testing.T) {
	wk, _, cmds := newTestWK(t)

	wk.processByte('E')
	wk.processByte('T')
	wk.processByte(0x08) // backspace, inside sendLoop's 120ms coalescing window

	expectCmd(t, cmds, "KY E")
}
