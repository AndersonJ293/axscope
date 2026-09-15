package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

func (a *Agent) open(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	url := req.String("url")
	if url == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope open <url> [--new]"))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	if req.Bool("new", false) {
		tab, err := sess.NewTab(ctx, url)
		if err != nil {
			return protocol.Fail(err)
		}
		sid = tab.SessionID
	} else if err := sess.Navigate(ctx, sid, url, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	sess.UpdateHUD(ctx, "open "+url)
	title, _ := dom.EvalString(ctx, a.client(), sid, "document.title")
	return ok(fmt.Sprintf("ok: %s\n%s", url, title))
}

// wait waits for something to happen: a text to appear (or disappear, in
// waitgone), or a target to reach a state.
//
// The state exists because waiting for text does not cover the most common case
// of a real app: the button that only enables later. In lab v3, "Enviar
// candidatura" is released ~1.1s after appearing, without changing its text —
// and without this the way out was to inject setTimeout via eval.
//
// `within=` limits the text search to a container. Without a scope, text that
// also appears in an always-visible side menu matches before what is expected:
// measured in v3, waiting for a role came back in 2ms, with the search dropdown
// still closed.
func (a *Agent) wait(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	asked := req.String("text")
	if asked == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope %s <text|target> [timeout] [within=<target>]", req.Cmd))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	timeout := navTimeout
	if v := req.Int("timeout", 0); v > 0 {
		timeout = time.Duration(v) * time.Millisecond
	}
	start := time.Now()
	deadline := start.Add(timeout)

	if state := requestedState(req); state != "" {
		return a.waitForState(ctx, sess, sid, asked, state, start, deadline)
	}

	root, err := a.searchRoot(ctx, sess, sid, req.String("within"))
	if err != nil {
		return protocol.Fail(err)
	}

	present := req.Cmd == "wait"
	for time.Now().Before(deadline) {
		where, err := locateText(ctx, a.client(), sid, asked, root)
		if err == nil && (where != "") == present {
			verb := "appeared"
			if !present {
				verb = "disappeared"
			}
			msg := fmt.Sprintf("ok: %q %s in %dms", asked, verb, time.Since(start).Milliseconds())
			if where != "" {
				msg += " — at " + where
			}
			return ok(msg)
		}
		time.Sleep(120 * time.Millisecond)
	}
	if present {
		return protocol.Fail(fmt.Errorf("%q did not appear within %s", asked, timeout))
	}
	return protocol.Fail(fmt.Errorf("%q did not disappear within %s", asked, timeout))
}

// requestedState returns the state requested by flag, or "" when the wait is for
// text.
func requestedState(req protocol.Request) string {
	for _, s := range []string{"enabled", "visible", "gone"} {
		if req.Bool(s, false) {
			return s
		}
	}
	return ""
}

// searchRoot returns the objectId where the text search starts: the container
// requested in `within=`, or the document.
func (a *Agent) searchRoot(ctx context.Context, sess *browser.Session, sid, within string) (string, error) {
	if within == "" {
		return dom.EvalObject(ctx, a.client(), sid, "document")
	}
	t, _, err := a.resolve(ctx, sess, within)
	if err != nil {
		return "", fmt.Errorf("within=%s: %w", within, err)
	}
	return t.ObjectID, nil
}

// waitForState waits for the target to reach the requested state: "enabled"
// (accepts a click), "visible" (exists and has a box on screen) or "gone" (it
// stopped resolving).
func (a *Agent) waitForState(ctx context.Context, sess *browser.Session, sid, target, state string, start, deadline time.Time) protocol.Response {
	if state == "gone" {
		// Check that it exists first: otherwise a misspelled target "goes away"
		// immediately, and the response would confirm what never existed.
		if _, _, err := a.resolve(ctx, sess, target); err != nil {
			return protocol.Fail(fmt.Errorf("%s does not resolve, so there is nothing to disappear: %w", target, err))
		}
	}
	for time.Now().Before(deadline) {
		if arrived, err := a.atState(ctx, sess, target, state); err == nil && arrived {
			return ok(fmt.Sprintf("ok: %s %s in %dms", target, statePast(state), time.Since(start).Milliseconds()))
		}
		time.Sleep(120 * time.Millisecond)
	}
	return protocol.Fail(fmt.Errorf("%s failed to %s in %dms", target, stateBase(state), time.Since(start).Milliseconds()))
}

// atState says whether the target is in the requested state.
func (a *Agent) atState(ctx context.Context, sess *browser.Session, target, state string) (bool, error) {
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		// Failing to resolve is what "gone" expects — and only that.
		return state == "gone", nil
	}
	switch state {
	case "enabled":
		return browser.Enabled(ctx, a.client(), sid, t.ObjectID), nil
	case "visible":
		// It resolved and has a box: that is what "visible" means here (if it is
		// on the point, the click handles that, refusing the opposite).
		return true, nil
	}
	return false, nil
}

func statePast(state string) string {
	switch state {
	case "enabled":
		return "became enabled"
	case "visible":
		return "became visible"
	default:
		return "disappeared"
	}
}

func stateBase(state string) string {
	switch state {
	case "enabled":
		return "become enabled"
	case "visible":
		return "become visible"
	default:
		return "disappear"
	}
}

func (a *Agent) history(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	delta := -1
	if req.Cmd == "forward" {
		delta = 1
	}
	if err := sess.HistoryMove(ctx, sid, delta, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	return ok(fmt.Sprintf("ok: %s\nurl: %s", req.Cmd, url))
}

func (a *Agent) reload(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	if err := sess.Reload(ctx, sid, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	return ok("ok: reloaded")
}

// pageHasText says whether the page body contains the requested text.
// locateText says whether the text is on the page and, if so, describes where.
//
// Returning only "found" misleads: the text may already exist elsewhere and the
// wait come back in 1ms. It happened in lab mission 3, waiting for "Barreiras" —
// which the mission list itself already mentioned. Saying where it found it lets
// the agent check whether it is the place it wanted.
// textLocator searches for the text starting from the object where the function
// runs — the document, or the scope requested in `within=` — and describes where
// it found it.
//
// It scans open shadow roots and same-origin iframes: the read already shows the
// content of both, so the wait sees the same — otherwise the agent sees the text
// and cannot wait for it.
func textLocator(want string) string {
	return `function () {
		const want = ` + strconv.Quote(want) + `;
		const roots = [];
		const walk = (root, inFrame) => {
			roots.push([root, inFrame]);
			for (const el of root.querySelectorAll('*')) {
				if (el.shadowRoot) walk(el.shadowRoot, inFrame);
				if (el.tagName === 'IFRAME') {
					try { if (el.contentDocument) walk(el.contentDocument, true); } catch (e) {}
				}
			}
		};
		walk(this, false);
		// The smallest element that contains the text is the most likely
		// candidate to be "the" target — same logic as the leftover tiebreak in
		// the text aim.
		let best = null, slack = Infinity, inFrame = false;
		for (const pair of roots) {
			const root = pair[0], body = root.body;
			// The cheap stop holds for a document; a shadow root has no body and
			// is small, so it is not even worth it.
			if (body && !(body.innerText || '').includes(want)) continue;
			for (const el of root.querySelectorAll('*')) {
				const t = el.innerText || '';
				if (!t.includes(want)) continue;
				const s = t.trim().length - want.length;
				if (s < slack) { slack = s; best = el; inFrame = pair[1]; }
			}
		}
		if (!best) return '';
		let mark = '';
		if (best.id) mark = '#' + best.id;
		else if (typeof best.className === 'string' && best.className.trim())
			mark = '.' + best.className.trim().split(/\s+/)[0];
		const snippet = (best.innerText || '').trim().slice(0, 60);
		return best.tagName.toLowerCase() + mark + (inFrame ? ' (inside iframe)' : '') + ' — "' + snippet + '"';
	}`
}

// locateText runs the search starting from the given root (the document, from
// Runtime.evaluate, or the container from `within=`) and returns the description
// of where it found it — empty when it did not find it.
func locateText(ctx context.Context, client *cdp.Client, session, want, root string) (string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            root,
		"functionDeclaration": textLocator(want),
		"returnByValue":       true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	return res.Result.Value, nil
}
