package webui

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// jsFunc returns the source of a top-level `function name(...) {...}` in
// webui.html, matched by braces. The functions it is used on have no braces
// inside strings or comments.
func jsFunc(t *testing.T, name string) string {
	t.Helper()
	b, err := webuiHTML.ReadFile("webui.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	start := strings.Index(html, "\nfunction "+name+"(")
	if start < 0 {
		t.Fatalf("webui.html has no function %s", name)
	}
	start++
	depth := 0
	for i := start; i < len(html); i++ {
		switch html[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return html[start : i+1]
			}
		}
	}
	t.Fatalf("function %s is not closed", name)
	return ""
}

// runJS runs the named webui.html functions plus body under node, with input
// on stdin, and returns stdout. It skips the test when node isn't installed.
func runJS(t *testing.T, fns []string, body string, input any) []byte {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	var src strings.Builder
	for _, fn := range fns {
		src.WriteString(jsFunc(t, fn))
		src.WriteString("\n")
	}
	src.WriteString(body)
	in, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", src.String())
	cmd.Stdin = strings.NewReader(string(in))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("node: %v", err)
	}
	return out
}

// TestPowerSaveRow runs the page's Power-save row code under node against a
// stub psState element and checks the text and color it leaves there.
func TestPowerSaveRow(t *testing.T) {
	const body = `
const el = {className: '', textContent: ''};
const document = {getElementById: () => el};
const cases = JSON.parse(require('fs').readFileSync(0, 'utf8'));
console.log(JSON.stringify(cases.map(c => {
  renderPowerSave(c.ps, c.age);
  return {text: el.textContent, class: el.className};
})));
`

	cases := []struct {
		name  string
		ps    string // the powersave block as hearth sends it; "null" for none
		age   int    // replica push age; 0 from the Pi
		text  string // want; "" skips the text check
		amber bool
	}{
		{name: "held by k4", ps: `{"enabled":true,"armed":true,"phase":"down","window":"day",
			"next_up_at":"2026-09-27T16:00:00Z","relay":"starlink_mains","held_by":"k4"}`,
			text: "armed · held up: K4 on", amber: true},
		{name: "label from rig name", ps: `{"enabled":true,"armed":true,"phase":"down","held_by":"k3"}`,
			text: "armed · held up: K3 on", amber: true},
		{name: "fresh push", ps: `{"enabled":true,"armed":true,"phase":"down","held_by":"k4"}`, age: 120,
			text: "armed · held up: K4 on", amber: true},
		{name: "stale push", ps: `{"enabled":true,"armed":true,"phase":"down","held_by":"k4"}`, age: 121,
			text: "armed · held up: K4 on (as of last contact)", amber: true},
		{name: "held_by missing", ps: `{"enabled":true,"armed":true,"phase":"down","next_up_at":"2026-09-27T16:00:00Z"}`},
		{name: "held_by empty", ps: `{"enabled":true,"armed":true,"phase":"up","held_by":""}`, text: "armed · up"},
		{name: "disarmed", ps: `{"enabled":true,"armed":false,"phase":"up"}`, text: "disarmed"},
		{name: "no block", ps: `null`, text: "—"},
		// The replica rewrites desired to the Pi's own state once they agree:
		// a desired matching armed is not queued.
		{name: "replica desired matches armed", ps: `{"enabled":true,"armed":true,"phase":"down","held_by":"k4",
			"desired":"on","desired_at":"2026-09-27T19:37:13Z"}`, age: 41,
			text: "armed · held up: K4 on", amber: true},
		{name: "replica desired matches disarmed", ps: `{"enabled":true,"armed":false,"phase":"down","desired":"off"}`,
			text: "disarmed"},
		{name: "replica disarm pending", ps: `{"enabled":true,"armed":true,"phase":"up","desired":"off","desired_by":"K1ABC"}`,
			text: "armed · up · disarm queued by K1ABC"},
	}
	type jsCase struct {
		PS  json.RawMessage `json:"ps"`
		Age int             `json:"age"`
	}
	in := make([]jsCase, len(cases))
	for i, c := range cases {
		in[i] = jsCase{PS: json.RawMessage(c.ps), Age: c.age}
	}
	out := runJS(t, []string{"fmtAge", "ageSec", "fmtClock", "psHeld", "psPending", "describePowerSave", "renderPowerSave"}, body, in)
	var got []struct {
		Text  string `json:"text"`
		Class string `json:"class"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("node output %q: %v", out, err)
	}
	for i, c := range cases {
		g := got[i]
		if c.text != "" && g.Text != c.text {
			t.Errorf("%s: text = %q, want %q", c.name, g.Text, c.text)
		}
		if held := strings.Contains(g.Text, "held up"); held != c.amber {
			t.Errorf("%s: text = %q, held up shown = %v, want %v", c.name, g.Text, held, c.amber)
		}
		if c.amber && strings.Contains(g.Text, "until") {
			t.Errorf("%s: text = %q, want no countdown while held", c.name, g.Text)
		}
		wantClass := "station-value"
		if c.amber {
			wantClass += " warn"
		}
		if g.Class != wantClass {
			t.Errorf("%s: class = %q, want %q", c.name, g.Class, wantClass)
		}
	}
}

// TestReplicaSourceLine checks the source line shown when Sparks reads the
// replica: while a rig holds the dish up the cabin isn't dark, so the line
// must not say so or give a next-window time beside "held up".
func TestReplicaSourceLine(t *testing.T) {
	const body = `
const cases = JSON.parse(require('fs').readFileSync(0, 'utf8'));
console.log(JSON.stringify(cases.map(describeReplicaSource)));
`
	cases := []struct {
		name   string
		status string
		want   string // prefix
		dark   bool
	}{
		{name: "held, fresh push", status: `{"source":"replica","push_age_sec":5,
			"powersave":{"enabled":true,"armed":true,"phase":"down","next_up_at":"2026-09-27T16:00:00Z","held_by":"k4"}}`,
			want: "Replica · last heard 5s ago"},
		{name: "armed and down", status: `{"source":"replica","push_age_sec":2520,
			"powersave":{"enabled":true,"armed":true,"phase":"down","next_up_at":"2026-09-27T16:00:00Z"}}`,
			want: "Replica · cabin dark, last heard 42m ago, next window ", dark: true},
		{name: "no powersave block", status: `{"source":"replica","push_age_sec":60}`,
			want: "Replica · cabin dark, last heard 60s ago", dark: true},
	}
	in := make([]json.RawMessage, len(cases))
	for i, c := range cases {
		in[i] = json.RawMessage(c.status)
	}
	out := runJS(t, []string{"fmtAge", "fmtClock", "psHeld", "describeReplicaSource"}, body, in)
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("node output %q: %v", out, err)
	}
	for i, c := range cases {
		if !strings.HasPrefix(got[i], c.want) {
			t.Errorf("%s: line = %q, want prefix %q", c.name, got[i], c.want)
		}
		if dark := strings.Contains(got[i], "cabin dark"); dark != c.dark {
			t.Errorf("%s: line = %q, says cabin dark = %v, want %v", c.name, got[i], dark, c.dark)
		}
		if !c.dark && strings.Contains(got[i], "next window") {
			t.Errorf("%s: line = %q, want no next-window time while held", c.name, got[i])
		}
	}
}

// TestArmConfirmText checks the question asked before arming: with the K4
// reading on in a down phase, hearth holds the dish up, so it must not say
// the link drops immediately.
func TestArmConfirmText(t *testing.T) {
	const body = `
const cases = JSON.parse(require('fs').readFileSync(0, 'utf8'));
console.log(JSON.stringify(cases.map(armConfirmText)));
`
	cases := []struct {
		name, status, want, notWant string
	}{
		{name: "K4 on, down phase", status: `{"source":"pi","k4":{"card_state":"on"},
			"powersave":{"enabled":true,"armed":false,"phase":"down","next_up_at":"2026-09-27T20:00:00Z"}}`,
			want: "The K4 is on, so the dish stays up while it reads on.", notWant: "IMMEDIATELY"},
		{name: "K4 off, down phase", status: `{"source":"pi","k4":{"card_state":"unreachable"},
			"powersave":{"enabled":true,"armed":false,"phase":"down","next_up_at":"2026-09-27T20:00:00Z"}}`,
			want: "The dish drops IMMEDIATELY", notWant: "stays up"},
		{name: "no K4 block", status: `{"source":"pi","powersave":{"enabled":true,"armed":false,"phase":"down"}}`,
			want: "gone until the next window."},
		{name: "replica", status: `{"source":"replica","k4":{"card_state":"on"},"powersave":{"enabled":true,"armed":false}}`,
			want: "Queue arm?"},
	}
	in := make([]json.RawMessage, len(cases))
	for i, c := range cases {
		in[i] = json.RawMessage(c.status)
	}
	out := runJS(t, []string{"fmtClock", "armConfirmText"}, body, in)
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("node output %q: %v", out, err)
	}
	for i, c := range cases {
		if !strings.Contains(got[i], c.want) {
			t.Errorf("%s: text = %q, want it to contain %q", c.name, got[i], c.want)
		}
		if c.notWant != "" && strings.Contains(got[i], c.notWant) {
			t.Errorf("%s: text = %q, must not contain %q", c.name, got[i], c.notWant)
		}
	}
}

// TestK4PowerOnWaitClears checks that the pulse-power message clears once the
// Pi reads the K4 on, stays while it doesn't or while only the replica says
// on (its snapshot can predate the pulse), and that another message isn't
// cleared.
func TestK4PowerOnWaitClears(t *testing.T) {
	const body = `
const el = {className: '', textContent: ''};
const document = {getElementById: () => el};
const out = [];
function pulse() { setStationMsg('Pulsed power — waiting for the rig to come up', false); k4PowerOnWait = true; }
const pi = st => ({source: 'pi', k4: {card_state: st}});
pulse();
endK4PowerOnWait(pi('unreachable')); out.push(el.textContent);
endK4PowerOnWait({source: 'replica', push_age_sec: 3600, k4: {card_state: 'on'}}); out.push(el.textContent);
endK4PowerOnWait({source: 'unreachable'}); out.push(el.textContent);
endK4PowerOnWait(pi('on')); out.push(el.textContent);
pulse(); setStationMsg('Power-save armed', false);
endK4PowerOnWait(pi('on')); out.push(el.textContent);
console.log(JSON.stringify(out));
`
	src := "let k4PowerOnWait = false;\n"
	out := runJS(t, []string{"setStationMsg", "endK4PowerOnWait"}, src+body, nil)
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("node output %q: %v", out, err)
	}
	const waiting = "Pulsed power — waiting for the rig to come up"
	want := []string{waiting, waiting, waiting, "", "Power-save armed"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d: message = %q, want %q", i, got[i], want[i])
		}
	}
}
