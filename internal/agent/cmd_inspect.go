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
// and how much fits; the accessibility tree does not carry scroll state. The
// horizontal amount appears only when the area scrolls sideways.
func scrollLine(snap *browser.Snapshot) string {
	var parts []string
	if p := snap.Page; p != nil && (p.Max > 1 || p.MaxX > 1) {
		parts = append(parts, scrollAreaLine(*p))
	}
	for _, r := range snap.ScrollAreas {
		if len(parts) >= maxScrollAreas {
			break
		}
		parts = append(parts, scrollAreaLine(r))
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

// scrollAreaLine formats one area: `123/456` vertically, `x78/900` horizontally.
// An area that does not scroll an axis omits it, so a vertical-only area keeps
// the format it had before horizontal scroll existed.
func scrollAreaLine(a browser.ScrollArea) string {
	var axes []string
	if a.Max > 1 {
		axes = append(axes, fmt.Sprintf("%d/%d", a.Pos, a.Max))
	}
	if a.MaxX > 1 {
		axes = append(axes, fmt.Sprintf("x%d/%d", a.PosX, a.MaxX))
	}
	return a.Name + " " + strings.Join(axes, " ")
}

// maxScrollAreas is how many areas enter the header before the "+N" summary.
const maxScrollAreas = 5

func (a *Agent) read(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	raw := req.String("selector")
	sel := raw
	if sel == "" {
		sel = "main, article, [role=main], #content, .content, body"
	}
	if req.Bool("table", false) {
		result, err := dom.EvalString(ctx, a.client(), sid, tableExpression(raw))
		if err != nil {
			return protocol.Fail(err)
		}
		var res struct {
			Text  string `json:"text"`
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(result), &res) != nil {
			return protocol.Fail(fmt.Errorf("could not read the table"))
		}
		if res.Error != "" {
			return protocol.Fail(fmt.Errorf("%s", res.Error))
		}
		url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
		return ok(fmt.Sprintf("url: %s\n\n%s", url, res.Text))
	}
	if req.Bool("links", false) {
		links, err := dom.EvalString(ctx, a.client(), sid, linksExpression(sel))
		if err != nil {
			return protocol.Fail(err)
		}
		links = strings.TrimRight(links, "\n")
		if links == "" {
			links = "(no links in scope)"
		}
		url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
		return ok(fmt.Sprintf("url: %s\n\n%s", url, links))
	}
	text, err := dom.EvalString(ctx, a.client(), sid, readExpression(sel))
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

// readExpression reads the rendered text of the scope. `innerText` is the honest
// reading of a subtree — it skips what is not rendered — but it stops at a shadow
// boundary, so the text of each open shadow root is spliced in after the light
// DOM (its order untouched, its text first). A modal rendered in a shadow root
// (LinkedIn's `#interop-outlet`) is otherwise invisible to `read`.
func readExpression(sel string) string {
	return fmt.Sprintf(`(() => {
		const el = document.querySelector(%s) || document.body;
		if (!el) return '';
		let out = el.innerText || '';
		const add = (host) => {
			for (const child of host.shadowRoot.children) {
				const t = child.innerText || '';
				if (t) out += '\n' + t;
			}
		};
		const walk = (root) => {
			for (const host of root.querySelectorAll('*')) {
				if (!host.shadowRoot) continue;
				add(host);
				walk(host.shadowRoot);
			}
		};
		if (el.shadowRoot) {
			add(el);
			walk(el.shadowRoot);
		}
		walk(el);
		return out;
	})()`, strconv.Quote(sel))
}

// linksExpression lists the links of the scope as `label — href`, resolving
// relative URLs (the `href` property is absolute) and dropping `javascript:`
// and the anchors without an href. Identical label+href pairs are listed once.
func linksExpression(sel string) string {
	return fmt.Sprintf(`(() => {
		const scope = document.querySelector(%s) || document.body;
		if (!scope) return '';
		const seen = new Set();
		const out = [];
		for (const a of scope.querySelectorAll('a[href]')) {
			const href = a.href;
			if (!href || href.startsWith('javascript:')) continue;
			let label = (a.getAttribute('aria-label') || a.innerText || a.getAttribute('title') || '').trim();
			label = label.replace(/\s+/g, ' ').slice(0, 80);
			if (!label) label = href;
			const line = label + ' — ' + href;
			if (seen.has(line)) continue;
			seen.add(line);
			out.push(line);
		}
		const capped = out.slice(0, 200);
		if (out.length > capped.length) capped.push('(... ' + (out.length - capped.length) + ' more)');
		return capped.join('\n');
	})()`, strconv.Quote(sel))
}

// tableExpression reads an HTML table as aligned rows: the cells of each `tr`
// (th/td), one row per line, columns padded to the widest cell. It answers JSON
// so a missing or non-table target is refused by name, not with prose. Cards
// built from divs have no such structure and stay out of scope.
func tableExpression(sel string) string {
	return fmt.Sprintf(`(() => {
		let table = null;
		const sel = %s;
		if (sel) {
			const el = document.querySelector(sel);
			if (!el) return JSON.stringify({ error: 'no element for selector ' + sel });
			table = el.tagName === 'TABLE' ? el : el.querySelector('table');
			if (!table) return JSON.stringify({ error: 'the selector matched no <table> (cards built from divs are not a table)' });
		} else {
			table = document.querySelector('table');
			if (!table) return JSON.stringify({ error: 'no <table> on the page' });
		}
		const clean = (s) => (s || '').replace(/\s+/g, ' ').trim();
		const rows = [...table.querySelectorAll('tr')]
			.map((tr) => [...tr.querySelectorAll('th,td')].map((cell) => clean(cell.innerText)))
			.filter((r) => r.length > 0);
		if (!rows.length) return JSON.stringify({ error: 'the <table> has no rows' });
		const cols = Math.max(...rows.map((r) => r.length));
		const widths = [];
		for (let c = 0; c < cols; c++) widths[c] = Math.max(...rows.map((r) => (r[c] || '').length));
		const pad = (s, w) => s + ' '.repeat(Math.max(0, w - s.length));
		const text = rows.map((r) => {
			const cells = [];
			for (let c = 0; c < cols; c++) cells.push(pad(r[c] || '', widths[c]));
			return cells.join('  ').replace(/\s+$/, '');
		}).join('\n');
		return JSON.stringify({ text: text });
	})()`, strconv.Quote(sel))
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
