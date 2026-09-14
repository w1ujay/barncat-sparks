package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestActionsIncludeKind: the page filters the knob's action list by kind and
// momentary, so /api/actions must carry both.
func TestActionsIncludeKind(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleActions(rec, httptest.NewRequest(http.MethodGet, "/api/actions", nil))

	var got []struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Momentary bool   `json:"momentary"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		kind      string
		momentary bool
	}{
		"vfo_tune": {"relative", false},
		"vfo_swap": {"button", false},
		"ptt":      {"button", true},
		"volume":   {"absolute", false},
	}
	seen := 0
	for _, a := range got {
		w, ok := want[a.Name]
		if !ok {
			continue
		}
		seen++
		if a.Kind != w.kind || a.Momentary != w.momentary {
			t.Errorf("%s: kind=%q momentary=%v, want %q %v", a.Name, a.Kind, a.Momentary, w.kind, w.momentary)
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of %d checked actions in /api/actions", seen, len(want))
	}
}
