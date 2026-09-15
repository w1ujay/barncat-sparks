package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIndexWiresKnob guards the knob checkbox, its config key, Knob rows,
// Learn and Direction on the Settings page.
func TestIndexWiresKnob(t *testing.T) {
	rec := httptest.NewRecorder()
	(&WebUI{}).handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`id="knobEnabled"`, `knob_enabled:`, `cfg.knob_enabled`,
		`'/api/knob/status'`, `'/api/knob/learn'`,
		`['knob','Knob']`, `function learnKnob(`, `function onTypeChange(`,
		`opts.direction`, `a.kind === 'relative'`, `!a.momentary`, `controlMaps.knob`,
		`not set up — replace its driver with WinUSB using Zadig`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("webui.html missing %s", want)
		}
	}
}
