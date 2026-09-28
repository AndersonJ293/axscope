package agent

import (
	"strings"
	"testing"
)

// An agent writes steps in whichever form is at hand; both must parse to the
// same request, and the report must echo them the same way.
func TestParseStepsAcceptsLinesAndObjects(t *testing.T) {
	steps, err := parseSteps([]any{
		"fill css=#q 'hello world'",
		map[string]any{"cmd": "click", "target": "e3", "double": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("got %d steps", len(steps))
	}
	if s := steps[0]; s.err != nil || s.req.Cmd != "fill" || s.req.String("value") != "hello world" {
		t.Errorf("line step = %+v", s)
	}
	if s := steps[1]; s.err != nil || s.req.Cmd != "click" || !s.req.Bool("double", false) {
		t.Errorf("object step = %+v", s)
	}
	if got := steps[1].line; got != "click --double target=e3" {
		t.Errorf("object step line = %q", got)
	}
}

// From the CLI the steps arrive as text: a JSON list, or plain lines.
func TestParseStepsFromText(t *testing.T) {
	steps, err := parseSteps(`["press Enter", "snap"]`)
	if err != nil || len(steps) != 2 || steps[1].req.Cmd != "snap" {
		t.Fatalf("json text: %v %+v", err, steps)
	}
	steps, err = parseSteps("# login\npress Tab\n\npress Enter\n")
	if err != nil || len(steps) != 2 {
		t.Fatalf("lines: %v %+v", err, steps)
	}
}

// A typo in an argument name must be refused, not silently dropped: a fill
// with "text" instead of "value" would type nothing and answer ok.
func TestObjectStepRefusesUnknownArgument(t *testing.T) {
	s := objectStep(map[string]any{"cmd": "fill", "target": "e5", "text": "x"})
	if s.err == nil || !strings.Contains(s.err.Error(), `"text"`) {
		t.Errorf("err = %v", s.err)
	}
	if s := objectStep(map[string]any{"cmd": "nope"}); s.err == nil {
		t.Error("unknown command accepted")
	}
	if s := objectStep(map[string]any{"target": "e1"}); s.err == nil {
		t.Error("step without cmd accepted")
	}
}

func TestParseStepsRefusesNonList(t *testing.T) {
	if _, err := parseSteps(map[string]any{"cmd": "snap"}); err == nil {
		t.Error("an object is not a list of steps")
	}
	if _, err := parseSteps(`[broken`); err == nil {
		t.Error("broken JSON accepted")
	}
}

// Parse errors and nested batches fail without a browser, so the runner's
// report can be pinned: position, bail and the summary line.
func TestRunStepsBailsAndReports(t *testing.T) {
	a := &Agent{}
	steps, _ := parseSteps([]any{"ping", "nope", "ping"})
	res := a.runSteps(t.Context(), steps, true, "")
	if res.OK {
		t.Fatal("a failed step must fail the batch")
	}
	for _, want := range []string{"[1/3 ", "> ping", "pong", "[2/3 ", "!! unknown command", "stopped at step 2/3 (1 not run)"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("report does not say %q:\n%s", want, res.Error)
		}
	}

	res = a.runSteps(t.Context(), steps, false, "")
	if !strings.Contains(res.Error, "2/3 ok, 1 failed") {
		t.Errorf("--continue report:\n%s", res.Error)
	}

	steps, _ = parseSteps([]any{"ping", "script content=ping"})
	res = a.runSteps(t.Context(), steps, true, "")
	if !strings.Contains(res.Error, "cannot run inside a batch") {
		t.Errorf("nested script:\n%s", res.Error)
	}
}

func TestRunStepsSummaryWhenAllPass(t *testing.T) {
	steps, _ := parseSteps([]any{"ping", "ping"})
	res := (&Agent{}).runSteps(t.Context(), steps, true, "")
	if !res.OK || !strings.Contains(res.Text, "-- 2/2 ok in ") {
		t.Errorf("res = %+v", res)
	}
	if res := (&Agent{}).runSteps(t.Context(), steps, true, "full"); res.OK {
		t.Error("an unknown snap mode must be refused")
	}
}

func TestDropRepeatedURL(t *testing.T) {
	text, last := dropRepeatedURL("ok: fill e1\nurl: https://a/", "https://a/")
	if text != "ok: fill e1" || last != "https://a/" {
		t.Errorf("repeated url kept: %q", text)
	}
	text, last = dropRepeatedURL("ok: click e2\nurl: https://b/", last)
	if text != "ok: click e2\nurl: https://b/" || last != "https://b/" {
		t.Errorf("new url dropped: %q", text)
	}
}

func TestEvalTakesTheRestOfTheLine(t *testing.T) {
	cases := map[string]string{
		`eval () => document.title`:                   `() => document.title`,
		`eval document.querySelector('a b').href`:     `document.querySelector('a b').href`,
		`eval 'document.title'`:                       `document.title`,
		`eval "[...document.links].map(a => a.href)"`: `[...document.links].map(a => a.href)`,
		`eval () => 1 --raw`:                          `() => 1`,
	}
	for line, want := range cases {
		st := lineStep(line)
		if st.err != nil || st.req.String("js") != want {
			t.Errorf("%s: got %q (%v)", line, st.req.String("js"), st.err)
		}
	}
	if !lineStep(`eval () => 1 --raw`).req.Bool("raw", false) {
		t.Error("--raw is a flag, not JS")
	}
}

func TestCallIfFunction(t *testing.T) {
	for in, want := range map[string]string{
		`() => document.title`:     `(() => document.title)()`,
		`async () => { return 1 }`: `(async () => { return 1 })()`,
		`x => x`:                   `(x => x)()`,
		`function () { return 2 }`: `(function () { return 2 })()`,
		`document.title`:           `document.title`,
		`[1,2].map(x => x)`:        `[1,2].map(x => x)`,
		`(() => 1)()`:              `(() => 1)()`,
	} {
		if got := callIfFunction(in); got != want {
			t.Errorf("%s: got %s", in, got)
		}
	}
}
