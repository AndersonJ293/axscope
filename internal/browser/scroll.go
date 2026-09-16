// ScrollArea: the page, or a target's container. It goes in steps, so whoever
// watches follows the movement instead of the page jumping all at once.
package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Scroll scrolls the viewport by (dx, dy), in steps, returning where it stopped
// as "<who> <position>/<max>". It uses the scroller under the center, not a mouse
// wheel, and fires a scroll event on a hidden tab; `page` forces the document.
// When that scroller cannot move, it falls back to the largest scrollable area in
// view, and the answer names whichever moved.
func Scroll(ctx context.Context, client *cdp.Client, session string, dx, dy float64, page bool) (string, error) {
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    fmt.Sprintf("(%s)(%v, %v, %v)", scrollSteps, dx, dy, page),
		"returnByValue": true,
		"awaitPromise":  true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.Value == "" {
		return "", fmt.Errorf("could not scroll")
	}
	return res.Result.Value, nil
}

// ScrollTarget scrolls the target's container — the target itself, its nearest
// scrollable ancestor, or the document — and returns where it stopped.
func ScrollTarget(ctx context.Context, client *cdp.Client, session, objectID string, dx, dy float64) (string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": scrollTargetScript,
		"arguments": []any{
			map[string]any{"value": dx},
			map[string]any{"value": dy},
		},
		"returnByValue": true,
		"awaitPromise":  true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.Value == "" {
		return "", fmt.Errorf("could not scroll the target")
	}
	return res.Result.Value, nil
}

// jsDescribeScroll is pasted into both scroll scripts to describe where it
// stopped and to warn, at the page end on a hidden tab, what that prevents.
const jsDescribeScroll = `
	const describe = (el) => {
		const name = el === (document.scrollingElement || document.documentElement) ? 'page'
			: (el.id ? '#' + el.id : el.tagName.toLowerCase());
		const end = el.scrollTop >= (el.scrollHeight - el.clientHeight) - 1;
		const note = (end && document.hidden)
			? ' — end, and the tab is hidden: what loads via IntersectionObserver does not fire (use axscope tab <n> --focus)'
			: '';
		return name + ' ' + Math.round(el.scrollTop) + '/' + Math.round(el.scrollHeight - el.clientHeight) + note;
	};`

// scrollTargetScript scrolls the element's container, in steps.
const scrollTargetScript = `function (dx, dy) {` + jsDescribeScroll + `
	const scrollable = (el) => {
		if (!el || !el.scrollHeight) return false;
		const st = getComputedStyle(el);
		return /(auto|scroll|overlay)/.test(st.overflowY) && el.scrollHeight > el.clientHeight + 1;
	};
	let scroller = null;
	if (scrollable(this)) scroller = this;
	else {
		let c = this && this.parentElement;
		while (c) { if (scrollable(c)) { scroller = c; break; } c = c.parentElement; }
	}
	if (!scroller) scroller = document.scrollingElement || document.documentElement;
	const fromX = scroller.scrollLeft, fromY = scroller.scrollTop;
	const steps = (document.hidden || !scroller.scrollHeight) ? 1 : 6;
	let i = 0;
	return new Promise((done) => {
		const step = () => {
			i++;
			scroller.scrollTo({
				left: fromX + dx * (i / steps),
				top: fromY + dy * (i / steps),
				behavior: 'instant',
			});
			if (i < steps) { setTimeout(step, 35); return; }
			if (document.hidden) {
				scroller.dispatchEvent(new Event('scroll'));
				if (scroller === (document.scrollingElement || document.documentElement)) window.dispatchEvent(new Event('scroll'));
			}
			done(describe(scroller));
		};
		step();
	});
}`

// scrollSteps scrolls the scrollable element under the center of the screen, in
// steps; with `page`, it scrolls the document. When the default cannot move (the
// page is already at its end) it falls back to the largest scrollable area in
// view, so an inner list still moves; the answer names whichever moved.
const scrollSteps = `function (dx, dy, page) {` + jsDescribeScroll + `
	const doc = document.scrollingElement || document.documentElement;
	const scrollableY = (el) => {
		if (!el || !el.scrollHeight) return false;
		const st = getComputedStyle(el);
		return /(auto|scroll|overlay)/.test(st.overflowY) && el.scrollHeight > el.clientHeight + 1;
	};
	const canMove = (el) => {
		const max = el.scrollHeight - el.clientHeight;
		if (dy > 0) return el.scrollTop < max - 1;
		if (dy < 0) return el.scrollTop > 1;
		return false;
	};
	const inView = (el) => {
		const r = el.getBoundingClientRect();
		return r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth;
	};

	let scroller = doc;
	if (!page) {
		const cx = Math.round(innerWidth / 2), cy = Math.round(innerHeight / 2);
		const target = document.elementFromPoint(cx, cy);
		if (target) {
			let c = target;
			while (c) {
				if (scrollableY(c)) { scroller = c; break; }
				c = c.parentElement;
			}
		}
	}

	const run = (el) => new Promise((done) => {
		const fromX = el.scrollLeft, fromY = el.scrollTop;
		// A background tab throttles setTimeout, and animating for nobody only
		// makes the scroll slow: hidden, it goes all at once.
		const steps = document.hidden ? 1 : 6;
		let i = 0;
		const step = () => {
			i++;
			el.scrollTo({
				left: fromX + dx * (i / steps),
				top: fromY + dy * (i / steps),
				behavior: 'instant',
			});
			if (i < steps) { setTimeout(step, 35); return; }
			if (document.hidden) {
				el.dispatchEvent(new Event('scroll'));
				if (el === doc) window.dispatchEvent(new Event('scroll'));
			}
			done(Math.abs(el.scrollTop - fromY) > 1 || Math.abs(el.scrollLeft - fromX) > 1);
		};
		step();
	});

	return (async () => {
		if (await run(scroller)) return describe(scroller);
		// The default could not move: take the largest scrollable area in view
		// that can move in the requested direction, instead of scrolling nothing.
		if (page) return describe(scroller);
		let best = null, bestMax = 0;
		for (const el of document.querySelectorAll('*')) {
			if (el === doc || el === scroller) continue;
			// The cheap test first: getComputedStyle on every element is expensive.
			if (el.scrollHeight - el.clientHeight <= bestMax) continue;
			if (!scrollableY(el) || !canMove(el) || !inView(el)) continue;
			best = el;
			bestMax = el.scrollHeight - el.clientHeight;
		}
		if (!best) return describe(scroller);
		await run(best);
		return describe(best);
	})();
}`
