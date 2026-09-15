// ScrollArea: the page, or a target's container. It goes in steps, so whoever
// watches follows the movement instead of the page jumping all at once.
package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Scroll scrolls the viewport by (dx, dy). It goes in steps, so whoever watches
// follows the movement instead of the page jumping all at once.
//
// It scrolls by the scroller under the center of the screen, not by a mouse
// wheel: the wheel depends on who is under the pointer (on a page with a scroll
// box in the way, it scrolls the wrong container) and its ack from
// chrome.debugger sometimes does not come back — measured: 30s of timeout without
// scrolling anything.
//
// After scrolling, it notifies the page with a scroll event when the tab is
// hidden: in that condition the browser holds the delivery (it depends on the
// frame cycle, which does not run hidden) and whoever redraws on scroll —
// virtualized list, lazy loading — never learns that it scrolled. That was how
// the lab's virtualized list ended up with the DOM stopped at another point while
// the position was already the right one.
//
// It returns where it stopped, in the format "<who> <position>/<max>": the answer
// says what the scroll reached, not what was asked — with scroll-snap, or asking
// beyond the limit, the two differ. `page` forces the document, for when the
// target is nested and what one wants to scroll is the page.
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

// ScrollTarget scrolls the target's container — the target itself, if it
// scrolls, or the nearest scrollable ancestor; with none, the document. It
// returns where it stopped, like Scroll.
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

// jsDescribeScroll is pasted into both scroll scripts: it describes where it
// stopped and, at the end of the page with the tab hidden, warns what that
// prevents. The warning enters only at the end — it is when the absence of new
// content intrigues —, and not on every scroll, which would become noise in a
// case that is the normal one here.
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
// steps; with `page`, it scrolls the document.
const scrollSteps = `function (dx, dy, page) {` + jsDescribeScroll + `
	let scroller = document.scrollingElement || document.documentElement;
	if (!page) {
		const cx = Math.round(innerWidth / 2), cy = Math.round(innerHeight / 2);
		const target = document.elementFromPoint(cx, cy);
		if (target) {
			let c = target;
			while (c) {
				const st = getComputedStyle(c);
				if (/(auto|scroll|overlay)/.test(st.overflowY) && c.scrollHeight > c.clientHeight + 1) { scroller = c; break; }
				c = c.parentElement;
			}
		}
	}
	const fromX = scroller.scrollLeft, fromY = scroller.scrollTop;
	// A background tab throttles setTimeout, and animating for nobody only makes
	// the scroll slow: hidden, it goes all at once.
	const steps = document.hidden ? 1 : 6;
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
