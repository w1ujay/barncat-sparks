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

// TestPowerSaveRow runs the page's Power-save row code under node against a
// stub psState element and checks the text and color it leaves there.
func TestPowerSaveRow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	var src strings.Builder
	for _, fn := range []string{"fmtAge", "ageSec", "fmtClock", "psHeld", "describePowerSave", "renderPowerSave"} {
		src.WriteString(jsFunc(t, fn))
		src.WriteString("\n")
	}
	src.WriteString(`
const el = {className: '', textContent: ''};
const document = {getElementById: () => el};
const cases = JSON.parse(require('fs').readFileSync(0, 'utf8'));
console.log(JSON.stringify(cases.map(c => {
  renderPowerSave(c.ps, c.age);
  return {text: el.textContent, class: el.className};
})));
`)

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
	}
	type jsCase struct {
		PS  json.RawMessage `json:"ps"`
		Age int             `json:"age"`
	}
	in := make([]jsCase, len(cases))
	for i, c := range cases {
		in[i] = jsCase{PS: json.RawMessage(c.ps), Age: c.age}
	}
	input, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, "-e", src.String())
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("node: %v", err)
	}
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
