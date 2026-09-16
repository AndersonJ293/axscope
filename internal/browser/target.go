// Target resolution: ref, CSS selector, visible text or position become a target
// with geometry on screen, brought into reach with a visible scroll.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// Target is a resolved target ready to receive the action.
type Target struct {
	ObjectID      string
	BackendNodeID int
	Rect          dom.Rect
	Description   string
	// Point, when set, is where the action happens (a `pos=x,y` target); the
	// Rect remains the element's. Without separating them the action would fall
	// on the center, which inside an iframe drifts from the requested place.
	Point *Point
}

// Point is a screen coordinate.
type Point struct{ X, Y float64 }

// ResolveTarget resolves a reference into a target with geometry. Accepted forms
// are e12, css=..., text=... and pos=x,y.
func ResolveTarget(ctx context.Context, client *cdp.Client, session string, refs map[string]int, spec string) (*Target, error) {
	if spec == "" {
		return nil, fmt.Errorf("empty target")
	}

	var objectID string
	var backendID int
	var point *Point
	// Expression that produced the node, to resolve again if the scroll
	// invalidates it (a virtualized list recreates the rows).
	var expr string

	switch {
	case strings.HasPrefix(spec, "css="):
		sel := strings.TrimPrefix(spec, "css=")
		expr = cssExpression(sel)
		id, err := dom.EvalObject(ctx, client, session, expr)
		if err != nil {
			return nil, fmt.Errorf("invalid selector %q: %w", sel, err)
		}
		if id == "" {
			return nil, fmt.Errorf("no element for selector %q", sel)
		}
		objectID = id

	case strings.HasPrefix(spec, "text="):
		want := strings.TrimSpace(strings.TrimPrefix(spec, "text="))
		expr = textExpression(want)
		id, err := dom.EvalObject(ctx, client, session, expr)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fmt.Errorf(
				"no element with text %q — if the page loads on scroll, scroll down to the section and try again", want)
		}
		objectID = id

	case strings.HasPrefix(spec, "pos="):
		// Last resort for a target with no accessible name (a drag handle
		// without aria-label); it is position, not identity, and breaks easily.
		xy := strings.Split(strings.TrimPrefix(spec, "pos="), ",")
		if len(xy) != 2 {
			return nil, fmt.Errorf("malformed position %q — use pos=x,y", spec)
		}
		x, errX := strconv.ParseFloat(strings.TrimSpace(xy[0]), 64)
		y, errY := strconv.ParseFloat(strings.TrimSpace(xy[1]), 64)
		if errX != nil || errY != nil {
			return nil, fmt.Errorf("malformed position %q — use pos=x,y", spec)
		}
		expr = fmt.Sprintf("document.elementFromPoint(%v, %v)", x, y)
		id, err := dom.EvalObject(ctx, client, session, expr)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fmt.Errorf("nothing at %s (off screen?)", spec)
		}
		objectID = id
		point = &Point{X: x, Y: y}

	default:
		backend, ok := refs[spec]
		if !ok {
			return nil, fmt.Errorf("ref %q does not exist — run `snap` again (refs are per reading)", spec)
		}
		backendID = backend
		var res struct {
			Object struct {
				ObjectID string `json:"objectId"`
			} `json:"object"`
		}
		if err := client.SendJSON(ctx, "DOM.resolveNode",
			map[string]any{"backendNodeId": backend}, session, &res); err != nil {
			return nil, fmt.Errorf("ref %q no longer resolves (did the page change?) — run `snap` again", spec)
		}
		if res.Object.ObjectID == "" {
			return nil, fmt.Errorf("ref %q no longer resolves — run `snap` again", spec)
		}
		objectID = res.Object.ObjectID
	}

	t := &Target{ObjectID: objectID, BackendNodeID: backendID, Description: spec, Point: point}

	// Bring it to the screen in visible steps, then guarantee it with a direct
	// scroll — the action must reach the target.
	scrollResult := scrollIntoReach(ctx, client, session, objectID)
	scrolled := scrollResult != "already" && scrollResult != "no target"
	if scrollResult != "already" && scrollResult != "ok" {
		// Best effort; the box measurement below reports a failure.
		_ = dom.ScrollTo(ctx, client, session, objectID)
	}

	rect, err := dom.BoxOf(ctx, client, session, objectID)
	if err != nil && expr != "" {
		// A virtualized list recreates the rows as it scrolls, orphaning the
		// element resolved before (no box); resolve again by the same expression.
		if id, errEval := dom.EvalObject(ctx, client, session, expr); errEval == nil && id != "" && id != objectID {
			objectID = id
			rect, err = dom.BoxOf(ctx, client, session, objectID)
		}
	}
	if err != nil {
		// A recreated ref has no expression to re-resolve: the only way is a new
		// reading, and saying so separates a dead end from one more step.
		if expr == "" && backendID != 0 && scrolled {
			return nil, fmt.Errorf(
				"the scroll brought the target into view and the page recreated the element (a list that recycles rows?) — run `snap` again and use the new ref")
		}
		if want := requestedText(spec); want != "" {
			if msg := hiddenText(ctx, client, session, want); msg != "" {
				return nil, fmt.Errorf("%s", msg)
			}
		}
		return nil, fmt.Errorf("target %q has no visible area: %w", spec, err)
	}
	t.ObjectID = objectID
	t.Rect = rect
	return t, nil
}

// ResolveBackend resolves a backend node id to an object id, the reverse of the
// ref branch of ResolveTarget.
func ResolveBackend(ctx context.Context, client *cdp.Client, session string, backendID int) (string, error) {
	var res struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := client.SendJSON(ctx, "DOM.resolveNode",
		map[string]any{"backendNodeId": backendID}, session, &res); err != nil {
		return "", err
	}
	return res.Object.ObjectID, nil
}

// DescribeNode returns the backend node id behind a resolved object and a short
// selector (`tag#id.class`), so a target found by css=/text= can be matched back
// to the ref the reading gave it.
func DescribeNode(ctx context.Context, client *cdp.Client, session, objectID string) (int, string) {
	var res struct {
		Node struct {
			BackendNodeID int      `json:"backendNodeId"`
			NodeName      string   `json:"nodeName"`
			Attributes    []string `json:"attributes"`
		} `json:"node"`
	}
	if err := client.SendJSON(ctx, "DOM.describeNode",
		map[string]any{"objectId": objectID}, session, &res); err != nil {
		return 0, ""
	}
	name := strings.ToLower(res.Node.NodeName)
	id := ""
	var classes []string
	for i := 0; i+1 < len(res.Node.Attributes); i += 2 {
		switch res.Node.Attributes[i] {
		case "id":
			id = res.Node.Attributes[i+1]
		case "class":
			classes = strings.Fields(res.Node.Attributes[i+1])
		}
	}
	switch {
	case id != "":
		return res.Node.BackendNodeID, name + "#" + id
	case len(classes) > 0:
		if len(classes) > 2 {
			classes = classes[:2]
		}
		return res.Node.BackendNodeID, name + "." + strings.Join(classes, ".")
	default:
		return res.Node.BackendNodeID, name
	}
}

// ResolveObject resolves a target spec to a node without measuring it, so a
// hidden element — an `<input type=file>` behind a button — can still be reached.
// It takes the same forms as ResolveTarget, minus the geometry-based scroll.
func ResolveObject(ctx context.Context, client *cdp.Client, session string, refs map[string]int, spec string) (string, error) {
	switch {
	case strings.HasPrefix(spec, "css="):
		return dom.EvalObject(ctx, client, session, cssExpression(strings.TrimPrefix(spec, "css=")))
	case strings.HasPrefix(spec, "text="):
		want := strings.TrimSpace(strings.TrimPrefix(spec, "text="))
		return dom.EvalObject(ctx, client, session, textExpression(want))
	default:
		backend, ok := refs[spec]
		if !ok {
			return "", fmt.Errorf("ref %q does not exist — run `snap` again (refs are per reading)", spec)
		}
		return ResolveBackend(ctx, client, session, backend)
	}
}

// jsShortSelector defines `shortSelector(el)`; shared by DescribeNode's sibling
// and the per-node match, so a target and a listing describe the node the same.
const jsShortSelector = `
	const shortSelector = (el) => {
		const tag = (el.tagName || '?').toLowerCase();
		if (el.id) return tag + '#' + el.id;
		const cls = [...(el.classList || [])].slice(0, 2);
		return cls.length ? tag + '.' + cls.join('.') : tag;
	};`

// SelectorIfMatches says whether a resolved node matches a css=/text= target,
// returning its short selector when it does and "" otherwise. The text side
// shares the naming helper with textExpression, so the two agree.
func SelectorIfMatches(ctx context.Context, client *cdp.Client, session, objectID, spec string) string {
	var decl string
	var args []any
	switch {
	case strings.HasPrefix(spec, "css="):
		decl = `function (sel) {` + jsShortSelector + `
			if (!this.matches || !this.matches(sel)) return '';
			return shortSelector(this);
		}`
		args = []any{map[string]any{"value": strings.TrimPrefix(spec, "css=")}}
	case strings.HasPrefix(spec, "text="):
		want := strings.TrimSpace(strings.TrimPrefix(spec, "text="))
		decl = `function (want) {` + jsElementText + jsShortSelector + `
			const t = text(this);
			if (!t || !(t === want || t.includes(want))) return '';
			return shortSelector(this);
		}`
		args = []any{map[string]any{"value": want}}
	default:
		return ""
	}
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": decl,
		"arguments":           args,
		"returnByValue":       true,
	}, session)
	if err != nil {
		return ""
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.Result.Value
}

// underShadow walks inside open shadow roots too: the accessibility tree
// flattens shadow DOM, so a ref reaches the button while a DOM search must cross
// the shadow boundary to reach it.
const underShadow = `
	const underShadow = (root, sel, acc) => {
		for (const el of root.querySelectorAll(sel)) acc.push(el);
		for (const el of root.querySelectorAll('*')) {
			if (el.shadowRoot) underShadow(el.shadowRoot, sel, acc);
		}
		return acc;
	};`

// jsCandidates lists the elements that can be a text target.
const jsCandidates = `
	const sel = 'a,button,input,select,textarea,summary,[role],[tabindex],[aria-label],[contenteditable="true"],[draggable="true"],label,li,td,th,h1,h2,h3,p,span,div';`

// jsElementText says an element's text/name and whether it is in view, shared by
// the search and the count so the two agree.
const jsElementText = `
	const text = el => {
		const aria = (el.getAttribute('aria-label') || '').trim();
		if (aria) return aria;
		if (el.labels && el.labels.length) {
			const label = (el.labels[0].innerText || '').trim();
			if (label) return label;
		}
		const visible = (el.innerText || '').trim();
		if (visible) return visible;
		const hint = (el.getAttribute('placeholder') || el.getAttribute('title') || '').trim();
		if (hint) return hint;
		return (el.value || '').trim();
	};
	const hidden = el => {
		if (el.getClientRects().length === 0) return 1;
		return getComputedStyle(el).visibility === 'hidden' ? 1 : 0;
	};`

// jsCovered defines `covered(el)`: 1 when another layer sits over the element's
// center, 0 otherwise. It mirrors the click's own hit test (jsIsAtPoint) and
// crosses an open shadow boundary, so aiming by text does not pick the copy left
// under a modal overlay.
const jsCovered = `
	const covered = (el) => {
		const r = el.getBoundingClientRect();
		if (!r.width || !r.height) return 1;
		const over = el.ownerDocument.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
		if (!over) return 0;
		if (over === el || (el.contains && el.contains(over))) return 0;
		let n = el, crossed = false;
		while (n) {
			if (n === over) return (crossed || !!over.shadowRoot) ? 0 : 1;
			if (n.parentNode) { n = n.parentNode; continue; }
			const root = n.getRootNode ? n.getRootNode() : null;
			if (root && root.host) { n = root.host; crossed = true; continue; }
			return 1;
		}
		return 1;
	};`

// textExpression builds the search by visible text/accessible name, separated
// from ResolveTarget so the shadow-root crossing can be tested.
func textExpression(want string) string {
	return fmt.Sprintf(`(() => {
		const want = %s;
		%s
		%s
		%s
		%s
		const nodes = underShadow(document, sel, []);
		// "Actionable" breaks ties: the text lives in the <span>, but whoever
		// accepts the action is the <li draggable> / <a> around it. Without
		// that the target becomes the text.
		const actionable = el => el.matches('a,button,input,select,textarea,summary,[role],[tabindex],[contenteditable="true"],[draggable="true"]') || typeof el.onclick === 'function';
		// Preference order: exact name, then in view, then uncovered, then
		// tightest; actionable and outside the chrome break ties. Exact hidden
		// still beats partial visible, because aiming by text is aiming by name.
		// Covered comes before tightest so an overlay does not drag the aim to
		// the copy left under it.
		const betterThan = (a, b) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2] || a[3] - b[3] || a[4] - b[4] || a[5] - b[5];
		// A name colliding with the page chrome is a trap (two "Resources" menus);
		// outside the chrome wins the tie.
		const chrome = el => el.closest('nav,header,footer,[role="navigation"],[role="banner"],[role="contentinfo"]') ? 1 : 0;
		let chosen = null, key = null;
		for (const el of nodes) {
			const t = text(el);
			if (!t) continue;
			const exact = t === want;
			if (!exact && !t.includes(want)) continue;
			const current = [exact ? 0 : 1, hidden(el), covered(el), t.length - want.length, chrome(el), actionable(el) ? 0 : 1];
			if (key === null || betterThan(current, key) < 0) {
				chosen = el;
				key = current;
			}
		}
		return chosen;
	})()`, strconv.Quote(want), underShadow, jsCandidates, jsElementText, jsCovered)
}

// countExpression counts how many elements match the text and how many are in
// view, so the refusal can say why it failed.
func countExpression(want string) string {
	return fmt.Sprintf(`(() => {
		const want = %s;
		%s
		%s
		%s
		const all = underShadow(document, sel, []).filter(el => {
			const t = text(el);
			return t !== '' && (t === want || t.includes(want));
		});
		return { total: all.length, visible: all.filter(el => !hidden(el)).length };
	})()`, strconv.Quote(want), underShadow, jsCandidates, jsElementText)
}

// cssExpression tries the light document first, since that is the selector's
// semantics, falling back to shadow roots only when it finds nothing.
func cssExpression(sel string) string {
	target := strconv.Quote(sel)
	return fmt.Sprintf(`(() => {
		%s
		return document.querySelector(%s) || underShadow(document, %s, [])[0] || null;
	})()`, underShadow, target, target)
}

// scrollIntoReach scrolls in steps until the element is visible and returns
// "already", "ok", "miss" or "no target". It scrolls the `scrollTop` of the
// ancestor that actually scrolls, not a wheel at a fixed point.
func scrollIntoReach(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": scrollScript,
		"arguments":           []any{map[string]any{"value": 40}},
		"returnByValue":       true,
		"awaitPromise":        true,
	}, session)
	if err != nil {
		return "no target"
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return "no target"
	}
	return res.Result.Value
}

// scrollScript scrolls the element's scrollable ancestor, in steps, and confirms.
const scrollScript = `function (interval) {` + jsIsAtPoint + `
	const el = this;
	if (!el || !el.getBoundingClientRect) return Promise.resolve('no target');
	const margin = 60;
	// Visible is being at the point, the same question the click asks: a box on
	// screen can still be outside the window of the container that clips it.
	const visible = () => {
		const r = el.getBoundingClientRect();
		if (r.width === 0 || r.height === 0) return false;
		if (r.top < margin || r.bottom > innerHeight - margin) return false;
		return isAtPoint(el, r.left + r.width / 2, r.top + r.height / 2);
	};
	if (visible()) return Promise.resolve('already');

	const scrollable = (() => {
		let c = el.parentElement;
		while (c) {
			const st = getComputedStyle(c);
			if (/(auto|scroll|overlay)/.test(st.overflowY) && c.scrollHeight > c.clientHeight + 1) return c;
			c = c.parentElement;
		}
		return document.scrollingElement || document.documentElement;
	})();

	const from = scrollable.scrollTop;
	const r = el.getBoundingClientRect();
	// The center is the scroller's own, not the window's: a 420px container with
	// the window height in the calculation sends the target past the middle.
	const viewport = scrollable === (document.scrollingElement || document.documentElement)
		? { top: 0, height: innerHeight }
		: { top: scrollable.getBoundingClientRect().top, height: scrollable.clientHeight };
	const delta = r.top - (viewport.top + viewport.height / 2) + r.height / 2;
	// A background tab throttles setTimeout; hidden, it goes all at once.
	const steps = document.hidden ? 1 : 6;
	let i = 0;
	return new Promise((done) => {
		const step = () => {
			i++;
			// behavior 'instant' is mandatory: with scroll-behavior smooth in
			// the CSS, assigning scrollTop animates and the next step restarts
			// the animation, so the scroll never moves.
			scrollable.scrollTo({ top: from + delta * (i / steps), behavior: 'instant' });
			if (i < steps) { setTimeout(step, interval); return; }
			if (document.hidden) scrollable.dispatchEvent(new Event('scroll'));
			done(visible() ? 'ok' : 'miss');
		};
		step();
	});
}`

// requestedText returns the text of a `text=` target, or "" for the other forms.
func requestedText(spec string) string {
	if !strings.HasPrefix(spec, "text=") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(spec, "text="))
}

// hiddenText explains why a text target has no visible area when all candidates
// are hidden, for example a closed menu.
func hiddenText(ctx context.Context, client *cdp.Client, session, want string) string {
	raw, err := dom.Eval(ctx, client, session, countExpression(want))
	if err != nil {
		return ""
	}
	var res struct {
		Total   int `json:"total"`
		Visible int `json:"visible"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Total == 0 || res.Visible > 0 {
		return ""
	}
	return hiddenTextMessage(want, res.Total)
}

// hiddenTextMessage is the refusal sentence — separated so the test pins down
// what it needs to say: how many there are, and the next step.
func hiddenTextMessage(want string, total int) string {
	return fmt.Sprintf(
		"found %d elements with text %q and all of them are hidden — open what reveals them (a menu, a panel) and try again",
		total, want)
}

// actionPoint returns where the action happens: the requested point for a
// `pos=x,y` target, otherwise the element's center.
func (t *Target) actionPoint() (float64, float64) {
	if t.Point != nil {
		return t.Point.X, t.Point.Y
	}
	return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height/2
}
