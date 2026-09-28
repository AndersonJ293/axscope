package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/dom"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

const (
	// changeTimeout is shorter than a navigation's: a filter that answers in
	// ten seconds is broken, and an agent waiting for it should hear so.
	changeTimeout = 10 * time.Second
	// changeQuiet is how long the region must hold still to count as settled.
	changeQuiet = 300 * time.Millisecond
)

// observeJS waits on a DOM node. With requireChange it waits for the first
// mutation and then for `quiet` ms without one; without, it only waits for the
// quiet (no mutation during the window is already settled). It reports whether
// anything changed and whether the region still says it is loading (aria-busy).
// Attributes are limited to the ones that mean content, not animation: a
// spinner's class churn would never let a region settle.
const observeJS = `function (timeout, quiet, requireChange) {
	const root = this.nodeType === 9 ? this.body || this.documentElement : this;
	return new Promise(done => {
		const start = performance.now();
		let first = 0, last = 0;
		const seen = () => { const t = performance.now(); if (!first) first = t; last = t; };
		const obs = new MutationObserver(seen);
		obs.observe(root, { subtree: true, childList: true, characterData: true,
			attributes: true, attributeFilter: ['aria-busy', 'hidden', 'disabled', 'aria-hidden', 'aria-expanded', 'aria-selected', 'value'] });
		const busy = () => root.getAttribute && (root.getAttribute('aria-busy') === 'true' || !!root.querySelector('[aria-busy="true"]'));
		const tick = () => {
			const now = performance.now();
			const quietFor = now - (last || start);
			const waitingFirst = requireChange && !first;
			if (!waitingFirst && quietFor >= quiet && !busy()) {
				obs.disconnect();
				return done({ changed: !!first, after: first ? first - start : 0, busy: false, timedOut: false });
			}
			if (now - start >= timeout) {
				obs.disconnect();
				return done({ changed: !!first, after: first ? first - start : 0, busy: busy(), timedOut: true });
			}
			setTimeout(tick, 40);
		};
		setTimeout(tick, 40);
	});
}`

type observed struct {
	Changed  bool    `json:"changed"`
	After    float64 `json:"after"`
	Busy     bool    `json:"busy"`
	TimedOut bool    `json:"timedOut"`
}

func observe(ctx context.Context, a *Agent, sid, objectID string, timeout time.Duration, requireChange bool) (observed, error) {
	var res struct {
		Result struct {
			Value observed `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	raw, err := a.client().Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": observeJS,
		"arguments": []any{
			map[string]any{"value": timeout.Milliseconds()},
			map[string]any{"value": changeQuiet.Milliseconds()},
			map[string]any{"value": requireChange},
		},
		"awaitPromise":  true,
		"returnByValue": true,
	}, sid)
	if err != nil {
		return observed{}, err
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return observed{}, err
	}
	if res.ExceptionDetails != nil {
		return observed{}, fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	return res.Result.Value, nil
}

// waitChange waits for the page — or the within= region — to change and settle,
// then reads it, so "type into the search, then see the options it loaded" is one
// call with the refs in hand. Settled means: the DOM held still, the network went
// idle, the DOM still held still after that, and nothing says aria-busy. A region
// that does not exist yet (the listbox that opens on typing) is waited for first,
// and its appearing counts as the change.
func (a *Agent) waitChange(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	timeout := changeTimeout
	if req.String("text") != "" || req.Int("timeout", 0) > 0 {
		t, err := waitTimeout(req, "--change")
		if err != nil {
			return protocol.Fail(err)
		}
		timeout = t
	}
	start := time.Now()
	deadline := start.Add(timeout)
	remaining := func() time.Duration { return max(time.Until(deadline), 0) }
	within := req.String("within")

	objectID, appeared := "", false
	if within == "" {
		if objectID, err = dom.EvalObject(ctx, a.client(), sid, "document"); err != nil {
			return protocol.Fail(err)
		}
	} else {
		for {
			if id, err := browser.ResolveObject(ctx, a.client(), sid, a.currentRefs(), a.qualifyRef(within)); err == nil && id != "" {
				objectID = id
				break
			}
			if remaining() == 0 || ctx.Err() != nil {
				return protocol.Fail(fmt.Errorf("within=%s did not appear in %dms", within, time.Since(start).Milliseconds()))
			}
			appeared = true
			time.Sleep(80 * time.Millisecond)
		}
	}

	o, err := observe(ctx, a, sid, objectID, remaining(), !appeared)
	if err != nil {
		return a.changeRead(ctx, sess, within, fmt.Sprintf("ok: the page navigated while waiting (%dms)", time.Since(start).Milliseconds()))
	}
	changed := o.Changed || appeared
	if !changed {
		return protocol.Fail(fmt.Errorf("nothing changed%s in %dms", inRegion(within), time.Since(start).Milliseconds()))
	}
	firstAt := time.Duration(o.After) * time.Millisecond
	if appeared {
		firstAt = 0
	}

	// The DOM held still; a fetch in flight means the content is still coming
	// (a debounced search answers after its own quiet). Wait for the network,
	// then for the DOM once more, until both hold in the same round.
	pending := []string(nil)
	for !o.TimedOut && remaining() > 0 {
		var idle bool
		if pending, idle = sess.WaitForNetworkIdle(ctx, sid, changeQuiet, remaining()); !idle {
			break
		}
		if o, err = observe(ctx, a, sid, objectID, remaining(), false); err != nil || !o.Changed {
			break
		}
	}

	head := fmt.Sprintf("ok: changed%s after %dms, settled at %dms", inRegion(within), firstAt.Milliseconds(), time.Since(start).Milliseconds())
	switch {
	case o.Busy:
		head = fmt.Sprintf("ok: changed%s, still aria-busy at %dms (the timeout) — reading it as it is", inRegion(within), time.Since(start).Milliseconds())
	case len(pending) > 0:
		head += fmt.Sprintf(" (the network never went idle: %s)", strings.Join(firstN(pending, 2), ", "))
	case o.TimedOut:
		head = fmt.Sprintf("ok: changed%s but never held still before the timeout (%dms) — reading it as it is", inRegion(within), time.Since(start).Milliseconds())
	}
	return a.changeRead(ctx, sess, within, head)
}

// changeRead answers the wait with a reading of the region, which is what the
// agent waited for: the options, the results, the new rows — with refs.
func (a *Agent) changeRead(ctx context.Context, sess *browser.Session, within, head string) protocol.Response {
	args := map[string]any{}
	if within != "" {
		args["within"] = within
	}
	res := a.snap(ctx, sess, protocol.Request{Cmd: "snap", Args: args})
	if !res.OK {
		return ok(head + "\n!! snap: " + res.Error)
	}
	return ok(head + "\n\n" + res.Text)
}

func inRegion(within string) string {
	if within == "" {
		return ""
	}
	return " in " + within
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string(nil), s[:n]...), fmt.Sprintf("+%d more", len(s)-n))
}
