// Target resolution: ref, CSS selector, visible text or position become a target
// with geometry on screen — brought into reach with a visible scroll, so whoever
// watches follows instead of the page jumping.
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
	// Point, when it is not nil, is where the action should happen — the target
	// came from `pos=x,y`. The Rect remains the one of the element under the
	// point, for the highlight and for the hover to know where to enter from;
	// without separating the two, the action would fall on the element's center,
	// which in an iframe is dozens of pixels from the requested place.
	Point *Point
}

// Point is a screen coordinate.
type Point struct{ X, Y float64 }

// ResolveTarget resolves a reference into a target with geometry.
//
// Accepted forms:
//   - "e12"        → ref from the last `snap`
//   - "css=..."    → CSS selector
//   - "text=..."   → accessible name or visible text that matches
//   - "pos=x,y"    → the element under the point (last resort, for a target
//     without a name)
func ResolveTarget(ctx context.Context, client *cdp.Client, session string, refs map[string]int, spec string) (*Target, error) {
	if spec == "" {
		return nil, fmt.Errorf("empty target")
	}

	var objectID string
	var backendID int
	var point *Point
	// Expression that produced the node, to be able to resolve again if the
	// scroll invalidates what was resolved (a virtualized list recreates the
	// rows).
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
		// Last resort, for a target with no accessible name at all (a drag
		// handle without aria-label, for example). It is position, not identity:
		// it breaks easily.
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

	// Bring it to the screen in visible steps; if that is not enough, guarantee
	// it with a direct scroll — what cannot happen is the action not reaching
	// the target.
	scrollResult := scrollIntoReach(ctx, client, session, objectID)
	scrolled := scrollResult != "already" && scrollResult != "no target"
	if scrollResult != "already" && scrollResult != "ok" {
		_ = dom.ScrollTo(ctx, client, session, objectID)
	}

	rect, err := dom.BoxOf(ctx, client, session, objectID)
	if err != nil && expr != "" {
		// The scroll may have invalidated the node: a virtualized list recreates
		// the rows as it scrolls, and the element resolved before becomes an
		// orphan (no box). Resolve again by the same expression and measure
		// again.
		if id, errEval := dom.EvalObject(ctx, client, session, expr); errEval == nil && id != "" && id != objectID {
			objectID = id
			rect, err = dom.BoxOf(ctx, client, session, objectID)
		}
	}
	if err != nil {
		// A ref has no expression to re-resolve: its identity was the node, and
		// the node was recreated. The way is a new reading — and saying that is
		// what separates a dead end from one more step.
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

// underShadow walks inside open shadow roots too.
//
// The accessibility tree **flattens** shadow DOM: the reading shows the button
// that is in there, with name and ref, and the ref even reaches it (resolves by
// backendNodeId). Aiming by DOM does not — it walked only in the light document,
// so that the reading showed it and the `text=` did not reach it. Measured in
// mission 12 of the lab.
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

// jsElementText says an element's text/name and whether it is in view.
//
// Shared between the text search and the candidate count, so the two agree —
// counting by one criterion and choosing by another would be worse than not
// counting.
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

// textExpression builds the search by visible text/accessible name.
//
// Separated from ResolveTarget because it is testable — and the test exists to
// pin down that it crosses shadow root.
func textExpression(want string) string {
	return fmt.Sprintf(`(() => {
		const want = %s;
		%s
		%s
		%s
		const nodes = underShadow(document, sel, []);
		// "Actionable" breaks ties: the text lives in the <span>, but whoever
		// accepts the action is the <li draggable> / <a> around it. Without
		// that the target becomes the text.
		const actionable = el => el.matches('a,button,input,select,textarea,summary,[role],[tabindex],[contenteditable="true"],[draggable="true"]') || typeof el.onclick === 'function';
		// Preference order: exact name, then in view, then the tightest (least
		// leftover text); actionable only breaks ties. "In view" comes early on
		// purpose: a closed menu item matching before the visible one is what
		// made the click by text aim at a button of another menu. And it comes
		// after exact because aiming by text is aiming by name: an exact hidden
		// name still beats a partial visible name.
		const betterThan = (a, b) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2] || a[3] - b[3] || a[4] - b[4];
		// A name that collides with the page chrome is a trap: the LinkedIn "⋯"
		// menu is called "Resources", the same as the "Resources" at the top.
		// Outside the chrome wins the tie.
		const chrome = el => el.closest('nav,header,footer,[role="navigation"],[role="banner"],[role="contentinfo"]') ? 1 : 0;
		let chosen = null, key = null;
		for (const el of nodes) {
			const t = text(el);
			if (!t) continue;
			const exact = t === want;
			if (!exact && !t.includes(want)) continue;
			const current = [exact ? 0 : 1, hidden(el), t.length - want.length, chrome(el), actionable(el) ? 0 : 1];
			if (key === null || betterThan(current, key) < 0) {
				chosen = el;
				key = current;
			}
		}
		return chosen;
	})()`, strconv.Quote(want), underShadow, jsCandidates, jsElementText)
}

// countExpression counts how many elements match the text and how many are in
// view. It is what lets the refusal say why it failed, instead of only saying it
// failed — whoever reads learns whether they need to open a menu or a modal.
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

// cssExpression builds the search by CSS selector.
//
// The CSS selector has its own semantics in the light document, so it is tried
// first. Only when it finds nothing is it worth looking inside shadow roots:
// this way a page that always worked does not change its target, and one with a
// web component stops being a dead end.
func cssExpression(sel string) string {
	target := strconv.Quote(sel)
	return fmt.Sprintf(`(() => {
		%s
		return document.querySelector(%s) || underShadow(document, %s, [])[0] || null;
	})()`, underShadow, target, target)
}

// scrollIntoReach scrolls in steps until the element becomes visible, so whoever
// watches follows the movement instead of seeing the page jump. It returns what
// happened: "already" (it was already visible), "ok" (it scrolled and arrived),
// "miss" (it scrolled and did not arrive) or "no target".
//
// It scrolls by the `scrollTop` of the ancestor that actually scrolls, and not by
// a mouse wheel at a fixed point: the wheel goes to whoever is under the pointer,
// and on a page with a scrollable container in the way (an inner scroll box, a
// virtual list) it scrolls the wrong container — and the page does not move.
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

// scrollScript scrolls the element's scrollable ancestor, in steps, and
// confirms.
const scrollScript = `function (interval) {` + jsIsAtPoint + `
	const el = this;
	if (!el || !el.getBoundingClientRect) return Promise.resolve('no target');
	const margin = 60;
	// Visible is being at the point — the same question the click asks. The box
	// can be on screen and still outside the window of the container that clips
	// it (a virtualized list clips by overflow), and then the click happens
	// outside the target.
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
	// the window height in the calculation sends the target far beyond the
	// middle.
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
			// behavior 'instant' is mandatory: with scroll-behavior smooth in the
			// CSS, assigning scrollTop animates — and the next step restarts the
			// animation, so that the scroll never moves. The animation is ours,
			// in the steps above.
			scrollable.scrollTo({ top: from + delta * (i / steps), behavior: 'instant' });
			if (i < steps) { setTimeout(step, interval); return; }
			if (document.hidden) scrollable.dispatchEvent(new Event('scroll'));
			done(visible() ? 'ok' : 'miss');
		};
		step();
	});
}`

// actionPoint returns the exact point where the action happens: the requested
// point, when the target came from `pos=x,y`; otherwise the element's center.
// requestedText returns the text of a `text=` target, or "" for the other forms.
func requestedText(spec string) string {
	if !strings.HasPrefix(spec, "text=") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(spec, "text="))
}

// hiddenText explains why a text target has no visible area, when the cause is
// that all the candidates are hidden — the case of the closed menu, which before
// only yielded "no visible area" and left whoever reads without a next step.
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

func (t *Target) actionPoint() (float64, float64) {
	if t.Point != nil {
		return t.Point.X, t.Point.Y
	}
	return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height/2
}
