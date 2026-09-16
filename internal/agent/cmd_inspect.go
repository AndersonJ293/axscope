package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/dom"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

func (a *Agent) status(ctx context.Context, _ *browser.Session, _ protocol.Request) protocol.Response {
	a.mu.Lock()
	booted := a.sess != nil
	a.mu.Unlock()
	if !booted {
		return ok(fmt.Sprintf("session %q: browser not started yet", a.Session))
	}
	sess := a.mustSess()
	tabs := sess.Tabs()
	sid, _ := sess.ActiveSID()
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	title, _ := dom.EvalString(ctx, a.client(), sid, "document.title")
	var b strings.Builder
	fmt.Fprintf(&b, "session: %s\n", a.Session)
	fmt.Fprintf(&b, "url: %s\n", url)
	fmt.Fprintf(&b, "title: %s\n", title)
	fmt.Fprintf(&b, "tabs: %d\n", len(tabs))
	fmt.Fprintf(&b, "active refs: %d\n", len(a.currentRefs()))
	return ok(strings.TrimRight(b.String(), "\n"))
}

func (a *Agent) snap(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	gen := a.nextGen()
	snap, err := browser.TakeSnapshot(ctx, a.client(), sid, browser.SnapshotOptions{
		RefsOnly: req.Bool("refs", false),
		All:      req.Bool("all", false),
		Gen:      gen,
	})
	if err != nil {
		return protocol.Fail(err)
	}
	a.setRefs(snap.Refs, gen)
	sess.UpdateHUD(ctx, "snap")

	var b strings.Builder
	fmt.Fprintf(&b, "title: %s\n", snap.Title)
	fmt.Fprintf(&b, "url: %s\n", snap.URL)
	fmt.Fprintf(&b, "-- %d lines, %d refs%s\n", snap.Count, len(snap.Refs), scrollLine(snap))
	b.WriteString(snap.Text)
	if snap.Truncated {
		b.WriteString("\n(... truncated; use `--refs` to reduce)")
	}
	return ok(b.String())
}

// scrollLine summarizes where the scrolling areas are, how far they scrolled
// and how much fits; the accessibility tree does not carry scroll state.
func scrollLine(snap *browser.Snapshot) string {
	var parts []string
	if p := snap.Page; p != nil && p.Max > 1 {
		parts = append(parts, fmt.Sprintf("%s %d/%d", p.Name, p.Pos, p.Max))
	}
	for _, r := range snap.ScrollAreas {
		if len(parts) >= maxScrollAreas {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d/%d", r.Name, r.Pos, r.Max))
	}
	if len(parts) == 0 {
		return ""
	}
	line := " · scroll: " + strings.Join(parts, " · ")
	if rest := snap.ScrollAreasTotal - len(parts); rest > 0 {
		line += fmt.Sprintf(" (+%d)", rest)
	}
	return line
}

// maxScrollAreas is how many areas enter the header before the "+N" summary.
const maxScrollAreas = 5

func (a *Agent) read(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	sel := req.String("selector")
	if sel == "" {
		sel = "main, article, [role=main], #content, .content, body"
	}
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s) || document.body;
		return el ? el.innerText : '';
	})()`, strconv.Quote(sel))
	text, err := dom.EvalString(ctx, a.client(), sid, expr)
	if err != nil {
		return protocol.Fail(err)
	}
	text = squeeze(text)
	if len(text) > 8000 {
		text = text[:8000] + "\n(... truncated)"
	}
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	return ok(fmt.Sprintf("url: %s\n\n%s", url, text))
}

func (a *Agent) eval(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	js := req.String("js")
	if js == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope eval <js>"))
	}
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	raw, err := dom.EvalAwait(ctx, a.client(), sid, js)
	if err != nil {
		return protocol.Fail(err)
	}
	if req.Bool("raw", false) {
		return ok(rawValue(raw))
	}
	return ok(prettyValue(raw))
}

// prettyValue shows a CDP value the way a person reads it: a JSON string loses
// the quotes and escapes, an object/array is indented, and scalars keep their
// exact text (a float round-trip would corrupt big integers). --raw bypasses it.
func prettyValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	case '{', '[':
		if b, err := json.MarshalIndent(raw, "", "  "); err == nil {
			return string(b)
		}
	}
	return string(raw)
}

// rawValue is the exact CDP result.value, the previous behavior kept behind --raw.
func rawValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "undefined"
	}
	return string(raw)
}

func (a *Agent) console(_ context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	level := "error,warn"
	if req.Bool("all", false) {
		level = "all"
	}
	entries := sess.Observe.Console(sid, "", 0)
	var b strings.Builder
	count := 0
	for _, e := range entries {
		if level != "all" && e.Level != "error" && e.Level != "warn" {
			continue
		}
		count++
		loc := ""
		if e.URL != "" {
			loc = fmt.Sprintf(" (%s:%d)", e.URL, e.Line)
		}
		fmt.Fprintf(&b, "[%s] %s%s\n", e.Level, e.Text, loc)
	}
	if count == 0 {
		return ok("(no console errors/warnings)")
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}

func (a *Agent) net(_ context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	filter := req.String("filter")
	entries := sess.Observe.Network(sid, filter, 60)
	if len(entries) == 0 {
		return ok("(no requests)")
	}
	var b strings.Builder
	for _, e := range entries {
		if e.Failed != "" {
			fmt.Fprintf(&b, "FAIL %s %s (%s)\n", e.Method, e.URL, e.Failed)
			continue
		}
		fmt.Fprintf(&b, "%d %s %s\n", e.Status, e.Method, e.URL)
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}
