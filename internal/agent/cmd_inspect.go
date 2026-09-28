package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/dom"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

func (a *Agent) status(ctx context.Context, _ *browser.Session, _ protocol.Request) protocol.Response {
	a.mu.Lock()
	booted := a.sess != nil
	handle := a.handle
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
	fmt.Fprintf(&b, "engine: %s\n", a.engineName())
	if handle != nil {
		fmt.Fprintf(&b, "connection: %s\n", connectionState(handle))
		fmt.Fprintf(&b, "cdp: %s\n", cdpEndpoint(handle))
	}
	fmt.Fprintf(&b, "url: %s\n", url)
	fmt.Fprintf(&b, "title: %s\n", title)
	fmt.Fprintf(&b, "tabs: %d\n", len(tabs))
	for _, t := range tabs {
		marker := " "
		if t.Active {
			marker = "*"
		}
		label := t.Title
		if label == "" {
			label = "(untitled)"
		}
		fmt.Fprintf(&b, "%s[%d] %s — %s\n", marker, t.Index, label, t.URL)
	}
	a.mu.Lock()
	inFrames := len(a.frameRefs)
	a.mu.Unlock()
	fmt.Fprintf(&b, "active refs: %d\n", len(a.currentRefs())+inFrames)
	return ok(strings.TrimRight(b.String(), "\n"))
}

// engineName says how the session drives the browser, for status.
func (a *Agent) engineName() string {
	if a.Attach != "" {
		return "attached (" + a.Attach + ")"
	}
	if a.Engine == "" {
		return browser.EngineExt
	}
	return a.Engine
}

// connectionState reports whether the CDP connection is still alive, and names
// the recovery when it is not.
func connectionState(h *browser.Handle) string {
	if h.Client.Err() != nil || h.Exited() {
		return "down (the next command rebuilds the session)"
	}
	return "ok"
}

// cdpEndpoint names where the session's CDP lives, so an agent that wants to fall
// back to raw CDP does not have to reconstruct it. Extension mode has none: the
// CDP runs inside the user's browser through the extension.
func cdpEndpoint(h *browser.Handle) string {
	if h.WSURL == "" {
		return "inside the browser through the extension (no external endpoint)"
	}
	if h.Attached {
		return h.WSURL + " (attached)"
	}
	return h.WSURL
}

func (a *Agent) snap(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	opts := browser.SnapshotOptions{
		RefsOnly: req.Bool("refs", false),
		All:      req.Bool("all", false),
		Page:     req.Bool("page", false),
		Viewport: req.Bool("viewport", false),
	}
	if raw := req.String("depth"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return protocol.Fail(fmt.Errorf("depth=%q: use a whole number from 1 (1 = the top level only)", raw))
		}
		opts.MaxDepth = n
	}
	// The scope resolves against the refs of the reading the agent has, so it
	// happens before this reading replaces them.
	if within := req.String("within"); within != "" {
		backend, label, frame, err := a.scopeNode(ctx, sid, within)
		if err != nil {
			return protocol.Fail(fmt.Errorf("within=%s: %w", within, err))
		}
		opts.Scope, opts.ScopeLabel, opts.ScopeFrame = backend, label, frame
	}
	key := fmt.Sprintf("%v|%v|%v|%v|%d|%d|%s", opts.RefsOnly, opts.All, opts.Page, opts.Viewport, opts.MaxDepth, opts.Scope, opts.ScopeFrame)
	a.mu.Lock()
	prev := a.readings[sid]
	a.mu.Unlock()
	// Numbers are reused only on the same page: another document's nodes are
	// other elements, whatever their ids.
	tabURL := ""
	if tab, err := sess.Active(); err == nil {
		tabURL = tab.URL
	}
	if prev != nil && prev.url != tabURL {
		prev = nil
	}
	if prev != nil {
		opts.PrevRefs = prev.nums
		opts.PrevFrameRefs = prev.frameNums
	}
	gen := a.nextGen()
	opts.Gen = gen
	snap, err := browser.TakeSnapshot(ctx, a.client(), sid, opts)
	if err != nil {
		return protocol.Fail(err)
	}
	a.setRefs(snap.Refs, gen)
	a.mu.Lock()
	a.frameRefs = snap.FrameRefs
	a.mu.Unlock()
	lines := strings.Split(snap.Text, "\n")
	a.mu.Lock()
	if a.readings == nil {
		a.readings = map[string]*reading{}
	}
	nums, frameNums := refNumbers(snap.Refs), frameRefNumbers(snap.FrameRefs)
	if prev != nil {
		// A reading that saw part of the page (within=, --viewport, a modal)
		// keeps the numbers of what it left out: the next full reading gives
		// those elements the refs they had, instead of new ones.
		inherit(nums, prev.nums)
		inherit(frameNums, prev.frameNums)
	}
	a.readings[sid] = &reading{url: tabURL, key: key, gen: gen, lines: lines, nums: nums, frameNums: frameNums}
	a.mu.Unlock()
	sess.UpdateHUD(ctx, "snap")

	var b strings.Builder
	fmt.Fprintf(&b, "title: %s\n", snap.Title)
	fmt.Fprintf(&b, "url: %s\n", snap.URL)
	fmt.Fprintf(&b, "-- %d lines, %d refs%s\n", snap.Count, len(snap.Refs)+len(snap.FrameRefs), scrollLine(snap))
	if line := scopeLine(snap); line != "" {
		b.WriteString(line + "\n")
	}
	if snap.Offscreen > 0 {
		fmt.Fprintf(&b, "-- viewport only: %d targets off screen left out (scroll, or drop --viewport)\n", snap.Offscreen)
	}
	body := snap.Text
	if req.Bool("delta", false) {
		switch {
		case prev == nil:
			b.WriteString("-- delta: no earlier reading of this page — the full reading follows\n")
		case prev.key != key:
			b.WriteString("-- delta: the earlier reading had other options — the full reading follows\n")
		default:
			text, added, removed, fits := delta(prev.lines, lines)
			switch {
			case !fits:
				fmt.Fprintf(&b, "-- delta vs #%d: +%d -%d, most of the page changed — the full reading follows\n", prev.gen, added, removed)
			case added+removed == 0:
				fmt.Fprintf(&b, "-- delta vs #%d: no change (refs stay valid)", prev.gen)
				return ok(b.String())
			default:
				fmt.Fprintf(&b, "-- delta vs #%d: +%d -%d lines; unchanged lines keep their refs\n", prev.gen, added, removed)
				body = text
			}
		}
	}
	b.WriteString(body)
	if snap.Truncated {
		b.WriteString("\n(... truncated; use `--refs` to reduce)")
	}
	return ok(capReading(b.String(), snapCap()))
}

// scopeNode resolves a within= target to its DOM node without measuring it: a
// reading must not scroll the page, and a container can be off screen or
// partly hidden and still be worth reading. A ref inside a cross-origin iframe
// also returns the frame's session: its backend id is the frame's own.
func (a *Agent) scopeNode(ctx context.Context, sid, target string) (int, string, string, error) {
	target = a.qualifyRef(target)
	if _, gen, ok := refGen(target); ok && !strings.HasPrefix(target, "css=") && !strings.HasPrefix(target, "text=") {
		a.mu.Lock()
		current := a.snapGen
		a.mu.Unlock()
		if gen != current {
			return 0, "", "", fmt.Errorf("ref %q is from an old read (the current one is #%d) — run `snap` again", target, current)
		}
	}
	a.mu.Lock()
	fr, inFrame := a.frameRefs[target]
	a.mu.Unlock()
	if inFrame {
		return fr.Backend, target, fr.Session, nil
	}
	objectID, err := browser.ResolveObject(ctx, a.client(), sid, a.currentRefs(), target)
	if err != nil {
		return 0, "", "", err
	}
	backend, label := browser.DescribeNode(ctx, a.client(), sid, objectID)
	if backend == 0 {
		return 0, "", "", fmt.Errorf("the target has no DOM node to read from")
	}
	return backend, label, "", nil
}

// scopeLine says the reading is not the whole page. A modal chose the scope on
// its own, so the line names the way back to the page: an agent that does not
// know the rest was left out would conclude it is not there.
func scopeLine(snap *browser.Snapshot) string {
	switch {
	case snap.Scope == "":
		return ""
	case snap.ScopeAuto:
		return fmt.Sprintf("-- scope: %s — an open modal; the page behind it is left out (--page reads it all)", snap.Scope)
	default:
		return fmt.Sprintf("-- scope: %s", snap.Scope)
	}
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
		links, err := dom.EvalString(ctx, a.client(), sid, linksExpression(sel, req.String("match")))
		if err != nil {
			return protocol.Fail(err)
		}
		links = strings.TrimRight(links, "\n")
		if links == "" {
			links = "(no links in scope)"
			if m := req.String("match"); m != "" {
				links = fmt.Sprintf("(no link in scope matches %q)", m)
			}
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

// readExpression reads the rendered text of the scope, crossing open shadow
// roots: `innerText` stops at a shadow boundary, so the text of each root is
// spliced in after the light DOM. Inside a root, `innerText` falls back to
// `textContent` for nodes that are not rendered, which used to carry the CSS of a
// `<style>` and the fields of a closed step into the reading; those are skipped.
// A modal rendered in a shadow root (LinkedIn's `#interop-outlet`) is otherwise
// invisible to `read`.
func readExpression(sel string) string {
	return fmt.Sprintf(`(() => {
		const el = document.querySelector(%s) || document.body;
		if (!el) return '';
		const skip = { STYLE: 1, SCRIPT: 1, NOSCRIPT: 1, TEMPLATE: 1, HEAD: 1, META: 1, LINK: 1, TITLE: 1 };
		const rendered = (node) => node.checkVisibility
			? node.checkVisibility({ checkVisibilityCSS: true, checkOpacity: false })
			: node.getClientRects().length > 0;
		let out = el.innerText || '';
		const add = (host) => {
			for (const child of host.shadowRoot.children) {
				if (skip[child.tagName] || !rendered(child)) continue;
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
// and the anchors without an href. A URL is listed once, with its first
// label: a card links the same page from its title, logo and company name.
// match keeps the links whose label or URL contains one of its "|"-separated
// parts, ignoring case — the job links, not the site's navigation.
func linksExpression(sel, match string) string {
	return fmt.Sprintf(`(() => {
		const scope = document.querySelector(%s) || document.body;
		if (!scope) return '';
		const wants = %s.split('|').map(s => s.trim().toLowerCase()).filter(Boolean);
		const byHref = new Map();
		for (const a of scope.querySelectorAll('a[href]')) {
			const href = a.href;
			if (!href || href.startsWith('javascript:')) continue;
			let label = (a.getAttribute('aria-label') || a.innerText || a.getAttribute('title') || '').trim();
			label = label.replace(/\s+/g, ' ').slice(0, 80);
			const known = byHref.get(href);
			if (known !== undefined) {
				if (!known && label) byHref.set(href, label);
				continue;
			}
			byHref.set(href, label);
		}
		const out = [];
		for (const [href, label] of byHref) {
			if (wants.length) {
				const hay = (label + ' ' + href).toLowerCase();
				if (!wants.some(w => hay.includes(w))) continue;
			}
			out.push((label || href) + ' — ' + href);
		}
		const capped = out.slice(0, 200);
		if (out.length > capped.length) capped.push('(... ' + (out.length - capped.length) + ' more — narrow it with match= or a selector)');
		return capped.join('\n');
	})()`, strconv.Quote(sel), strconv.Quote(match))
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
	raw, err := dom.EvalAwait(ctx, a.client(), sid, callIfFunction(js))
	if err != nil {
		return protocol.Fail(err)
	}
	if req.Bool("raw", false) {
		return ok(rawValue(raw))
	}
	return ok(prettyValue(raw))
}

// functionJS matches JavaScript that is a function, not a value.
var functionJS = regexp.MustCompile(`^(async\s+)?(function\b|\([^()]*\)\s*=>|[\w$]+\s*=>)`)

// callIfFunction runs a function the agent wrote as the whole expression
// (`() => document.title`): evaluated as is, it answers the function itself,
// an empty object, and the agent reads that as "nothing on the page".
func callIfFunction(js string) string {
	trimmed := strings.TrimSpace(js)
	if functionJS.MatchString(trimmed) {
		return "(" + trimmed + ")()"
	}
	return js
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
