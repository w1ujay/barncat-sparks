package controls

import "testing"

// TestReleasePTTUnkeysHeldPTT: lost input events release a held PTT, keep
// the executor usable, and send nothing when PTT isn't held.
func TestReleasePTTUnkeysHeldPTT(t *testing.T) {
	r := &fakeRadio{}
	e := New(r)
	e.ReleasePTT("MIDI events lost")
	e.Handle(pttPress)
	e.ReleasePTT("MIDI events lost")
	e.Handle(pttPress)
	wantPTT(t, r, []bool{true, false, true})
}
