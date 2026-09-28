package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/command"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// step is one command of a batch: the line it came from (echoed back) and the
// request it parsed into, or the reason it could not be parsed.
type step struct {
	line string
	req  protocol.Request
	err  error
}

// batch runs several commands in one call. It is the fast path for an agent:
// one round trip instead of N, and one answer that says how far it got.
func (a *Agent) batch(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	steps, err := parseSteps(req.Args["steps"])
	if err != nil {
		return protocol.Fail(err)
	}
	return a.runSteps(ctx, steps, !req.Bool("continue", false), req.String("snap"))
}

// runSteps runs the steps in order and reports each with its position and time.
// bail stops at the first error; the report then says how many were skipped.
// snap=final appends a reading of the screen after the last step, so an agent
// that acts and then looks pays one call, not two.
func (a *Agent) runSteps(ctx context.Context, steps []step, bail bool, snap string) protocol.Response {
	if len(steps) == 0 {
		return protocol.Fail(fmt.Errorf("empty batch"))
	}
	switch snap {
	case "", "none", "final":
	default:
		return protocol.Fail(fmt.Errorf("snap=%q: use none or final", snap))
	}

	start := time.Now()
	var b strings.Builder
	total, failed, ran := len(steps), 0, 0
	lastURL := ""
	for i, s := range steps {
		if ctx.Err() != nil {
			break
		}
		ran++
		t0 := time.Now()
		var res protocol.Response
		switch {
		case s.err != nil:
			res = protocol.Fail(s.err)
		case s.req.Cmd == "script" || s.req.Cmd == "batch":
			res = protocol.Fail(fmt.Errorf("%s cannot run inside a batch", s.req.Cmd))
		default:
			res = a.dispatch(ctx, s.req)
		}
		fmt.Fprintf(&b, "[%d/%d %s] > %s\n", i+1, total, elapsed(time.Since(t0)), s.line)
		if res.OK {
			var text string
			text, lastURL = dropRepeatedURL(res.Text, lastURL)
			if text != "" {
				b.WriteString(text + "\n")
			}
			continue
		}
		failed++
		fmt.Fprintf(&b, "!! %s\n", res.Error)
		if bail {
			break
		}
	}

	switch {
	case ran < total:
		fmt.Fprintf(&b, "-- stopped at step %d/%d (%d not run) in %s", ran, total, total-ran, elapsed(time.Since(start)))
	case failed > 0:
		fmt.Fprintf(&b, "-- %d/%d ok, %d failed in %s", total-failed, total, failed, elapsed(time.Since(start)))
	default:
		fmt.Fprintf(&b, "-- %d/%d ok in %s", total, total, elapsed(time.Since(start)))
	}

	if snap == "final" && ctx.Err() == nil {
		res := a.dispatch(ctx, protocol.Request{Cmd: "snap", Args: map[string]any{}})
		if res.OK {
			b.WriteString("\n\n" + res.Text)
		} else {
			b.WriteString("\n!! snap: " + res.Error)
		}
	}
	// A failed batch is still an answer: the steps that ran are in the text, and
	// the agent needs them to know where to resume.
	return protocol.Response{OK: failed == 0, Text: b.String(), Error: errorWithReport(failed, b.String())}
}

// errorWithReport keeps the whole report on a failed batch: transports show
// Error, not Text, when OK is false.
func errorWithReport(failed int, report string) string {
	if failed == 0 {
		return ""
	}
	return "a step failed\n" + report
}

// dropRepeatedURL removes an action's `url:` line when the step left the page
// where the previous one did: in a batch it is the same line N times, and a URL
// that changed is the one worth reading.
func dropRepeatedURL(text, last string) (string, string) {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		if u, ok := strings.CutPrefix(l, "url: "); ok {
			if u == last {
				continue
			}
			last = u
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n"), last
}

// elapsed prints a duration short enough for a per-step prefix.
func elapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// parseSteps accepts what an agent naturally writes: a list whose items are
// either a command line ("click e3") or an object ({"cmd":"fill","target":"e5",
// "value":"x"}), or — from the CLI — that list as JSON text, or plain lines.
func parseSteps(raw any) ([]step, error) {
	if s, ok := raw.(string); ok {
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "[") {
			var list []any
			if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
				return nil, fmt.Errorf("steps is not a valid JSON list: %w", err)
			}
			raw = list
		} else {
			return linesToSteps(s), nil
		}
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf(`steps must be a list, e.g. ["click e3", {"cmd":"fill","target":"e5","value":"x"}]`)
	}
	out := make([]step, 0, len(list))
	for i, item := range list {
		switch v := item.(type) {
		case string:
			out = append(out, lineStep(strings.TrimSpace(v)))
		case map[string]any:
			out = append(out, objectStep(v))
		default:
			out = append(out, step{line: fmt.Sprintf("step %d", i+1), err: fmt.Errorf("a step is a command line or an object with cmd")})
		}
	}
	return out, nil
}

// linesToSteps is the script grammar: one command per line, # comments.
func linesToSteps(content string) []step {
	var out []step
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, lineStep(trimmed))
	}
	return out
}

func lineStep(line string) step {
	if st, ok := restStep(line); ok {
		return st
	}
	tokens, err := splitTokens(line)
	if err != nil {
		return step{line: line, err: err}
	}
	req, err := command.Parse(tokens)
	return step{line: line, req: req, err: err}
}

// restStep reads a command whose last argument is the rest of the line (eval)
// from the raw text, so the quotes and spaces inside the JavaScript survive. A
// whole argument in quotes keeps meaning what it meant: the text inside them.
func restStep(line string) (step, bool) {
	name, raw, _ := strings.Cut(line, " ")
	spec, ok := command.Lookup(name)
	if !ok || !spec.Rest || len(spec.Positional) != 1 {
		return step{}, false
	}
	raw = strings.TrimSpace(raw)
	args := map[string]any{}
	for {
		cut := false
		for _, f := range spec.Flags {
			if rest, found := strings.CutSuffix(raw, " --"+f); found {
				args[f], raw, cut = true, strings.TrimSpace(rest), true
			} else if rest, found := strings.CutPrefix(raw, "--"+f+" "); found {
				args[f], raw, cut = true, strings.TrimSpace(rest), true
			}
		}
		if !cut {
			break
		}
	}
	if raw == "" {
		return step{}, false
	}
	if q := raw[0]; (q == '\'' || q == '"') && raw[len(raw)-1] == q && len(raw) > 1 {
		if tokens, err := splitTokens(raw); err == nil && len(tokens) == 1 {
			raw = tokens[0]
		}
	}
	args[spec.Positional[0]] = raw
	return step{line: line, req: protocol.Request{Cmd: spec.Cmd, Args: args}}, true
}

// objectStep validates the object against the command table, so a typo in an
// argument name is refused instead of silently ignored.
func objectStep(obj map[string]any) step {
	cmd, _ := obj["cmd"].(string)
	args := map[string]any{}
	for k, v := range obj {
		if k != "cmd" {
			args[k] = v
		}
	}
	line := describe(cmd, args)
	if cmd == "" {
		return step{line: line, err: fmt.Errorf(`the step has no "cmd"`)}
	}
	spec, ok := command.Lookup(cmd)
	if !ok {
		return step{line: line, err: fmt.Errorf("unknown command: %q (see `help`)", cmd)}
	}
	known := map[string]bool{}
	for _, p := range spec.Positional {
		known[p] = true
	}
	for _, f := range spec.Flags {
		known[f] = true
	}
	for k := range args {
		if !known[k] {
			return step{line: line, err: fmt.Errorf("command %q has no %q argument", cmd, k)}
		}
	}
	return step{line: line, req: protocol.Request{Cmd: cmd, Args: args}}
}

// describe renders an object step as the command line it stands for, so the
// report reads the same whatever form the step came in.
func describe(cmd string, args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{cmd}
	for _, k := range keys {
		switch v := args[k].(type) {
		case bool:
			if v {
				parts = append(parts, "--"+k)
			}
		case string:
			if strings.ContainsAny(v, " \t") {
				v = fmt.Sprintf("%q", v)
			}
			parts = append(parts, k+"="+v)
		default:
			b, _ := json.Marshal(v)
			parts = append(parts, k+"="+string(b))
		}
	}
	return strings.Join(parts, " ")
}
