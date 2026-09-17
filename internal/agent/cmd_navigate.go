package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	var tab *browser.Tab
	if req.Bool("new", false) {
		tab, err = sess.NewTab(ctx, url)
		if err != nil {
			return protocol.Fail(err)
		}
		sid = tab.SessionID
	} else if err := sess.Navigate(ctx, sid, url, navTimeout, req.Bool("force", false)); err != nil {
		return protocol.Fail(err)
	}
	sess.UpdateHUD(ctx, "open "+url)
	title, _ := dom.EvalString(ctx, a.client(), sid, "document.title")
	line := fmt.Sprintf("ok: %s\n%s", url, title)
	if tab == nil {
		tab, _ = sess.Active()
	}
	if tab != nil {
		line += "\ntab: " + tabRef(sess, tab.TargetID)
	}
	return ok(line)
}

// wait waits for text to appear/disappear, or for a target to reach a state
// (--enabled/--visible/--gone) for controls that only enable later; `within=`
// limits the text search so an identical label elsewhere does not match first.
func (a *Agent) wait(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	if req.Bool("network-idle", false) {
		return a.waitNetworkIdle(ctx, sess, req)
	}
	if want, re := req.String("url"), req.String("urlre"); want != "" || re != "" {
		return a.waitURL(ctx, sess, req, want, re)
	}
	asked := req.String("text")
	if asked == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope %s <text|target> [timeout] [within=<target>] [url=/urlre=]", req.Cmd))
	}
	sid, err := sess.ActiveSID()
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
		if err := ctx.Err(); err != nil {
			return protocol.Fail(fmt.Errorf("%q wait cancelled: %w", asked, err))
		}
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

// waitURL waits for location.href to contain `want` or match `re`, and for
// `waitgone` for that to stop being true. A URL wait survives the text changing
// for unrelated reasons, which is what makes it reliable in an SPA.
func (a *Agent) waitURL(ctx context.Context, sess *browser.Session, req protocol.Request, want, re string) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	match, label, err := urlMatcher(want, re)
	if err != nil {
		return protocol.Fail(err)
	}
	timeout, err := waitTimeout(req, "url")
	if err != nil {
		return protocol.Fail(err)
	}
	present := req.Cmd == "wait"
	start := time.Now()
	href, matched := sess.WaitForURL(ctx, sid, present, match, timeout)
	done, undone := "appeared", "appear"
	if !present {
		done, undone = "went away", "go away"
	}
	if !matched {
		return protocol.Fail(fmt.Errorf("url %s did not %s within %s — the URL is now %s", label, undone, timeout, href))
	}
	return ok(fmt.Sprintf("ok: url %s %s in %dms", label, done, time.Since(start).Milliseconds()))
}

// waitNetworkIdle waits for the network to go quiet: a page that renders in
// cascades (a shell, then data, then a second fetch) is only done after the last
// response, which the text and URL waits cannot say.
func (a *Agent) waitNetworkIdle(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	timeout, err := waitTimeout(req, "network-idle")
	if err != nil {
		return protocol.Fail(err)
	}
	start := time.Now()
	pending, idle := sess.WaitForNetworkIdle(ctx, sid, 500*time.Millisecond, timeout)
	if idle {
		return ok(fmt.Sprintf("ok: network idle in %dms", time.Since(start).Milliseconds()))
	}
	if len(pending) == 0 {
		return protocol.Fail(fmt.Errorf("the network did not go idle within %s — recent activity kept it busy", timeout))
	}
	shown := pending
	if len(shown) > 5 {
		shown = shown[:5]
	}
	msg := fmt.Sprintf("the network did not go idle within %s — still in flight:\n%s", timeout, strings.Join(shown, "\n"))
	if n := len(pending) - len(shown); n > 0 {
		msg += fmt.Sprintf("\n(... %d more)", n)
	}
	return protocol.Fail(fmt.Errorf("%s", msg))
}

// waitTimeout reads the timeout from `timeout=` or from the second positional,
// which the grammar puts in `text` once a flag selects the mode; a non-number
// there is a mistake worth naming instead of ignoring.
func waitTimeout(req protocol.Request, mode string) (time.Duration, error) {
	if v := req.Int("timeout", 0); v > 0 {
		return time.Duration(v) * time.Millisecond, nil
	}
	if rest := strings.TrimSpace(req.String("text")); rest != "" {
		v, err := strconv.Atoi(rest)
		if err != nil {
			return 0, fmt.Errorf("in %s mode the second argument is the timeout in ms, got %q", mode, rest)
		}
		return time.Duration(v) * time.Millisecond, nil
	}
	return navTimeout, nil
}

// urlMatcher builds the predicate behind `wait url=`/`urlre=` and the label the
// response shows: a substring by default, a regular expression when asked.
func urlMatcher(want, re string) (func(string) bool, string, error) {
	if re != "" {
		compiled, err := regexp.Compile(re)
		if err != nil {
			return nil, "", fmt.Errorf("bad urlre %q: %w", re, err)
		}
		return compiled.MatchString, strconv.Quote(re), nil
	}
	return func(u string) bool { return strings.Contains(u, want) }, strconv.Quote(want), nil
}

// requestedState returns the state requested by flag, or "" for a text wait.
func requestedState(req protocol.Request) string {
	for _, s := range []string{"enabled", "visible", "gone"} {
		if req.Bool(s, false) {
			return s
		}
	}
	return ""
}

// searchRoot returns the objectId where the text search starts: the `within=`
// container, or the document.
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

// waitForState waits for the target to reach "enabled" (clickable), "visible"
// (exists with a box) or "gone" (stopped resolving).
func (a *Agent) waitForState(ctx context.Context, sess *browser.Session, sid, target, state string, start, deadline time.Time) protocol.Response {
	if state == "gone" {
		// Check that it exists first: otherwise a misspelled target "goes away"
		// immediately, and the response would confirm what never existed.
		if _, _, err := a.resolve(ctx, sess, target); err != nil {
			return protocol.Fail(fmt.Errorf("%s does not resolve, so there is nothing to disappear: %w", target, err))
		}
	}
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return protocol.Fail(fmt.Errorf("%s wait cancelled: %w", target, err))
		}
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
	sid, err := sess.ActiveSID()
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
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	if err := sess.Reload(ctx, sid, navTimeout, req.Bool("hard", false)); err != nil {
		return protocol.Fail(err)
	}
	if req.Bool("hard", false) {
		return ok("ok: reloaded (cache bypassed)")
	}
	return ok("ok: reloaded")
}

// textLocator searches for the text from where the function runs (document or
// `within=` scope), describes where it found it, and scans open shadow roots and
// same-origin iframes to match what read shows.
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

// locateText runs the search from the given root (the document or the `within=`
// container) and returns where it found it, empty when it did not.
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
