package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// mutationJS keeps a short log of the page's DOM changes (time, node), from the
// moment the document exists. `wait --change` needs it: an agent acts and then
// waits, and the change the action caused — a list opening on a click, a
// filter applied on typing — has usually happened by the time the wait starts.
// The attributes are the ones that mean content (as in the wait's own
// observer), so a spinner's class churn does not fill the log. The overlay
// lives in a shadow root, which this observer does not see.
const mutationJS = `(() => {
	if (window.__axscopeMut) return;
	const log = [];
	const rec = { log };
	new MutationObserver(list => {
		const t = Date.now();
		for (const m of list) log.push([t, m.target]);
		if (log.length > 500) log.splice(0, log.length - 500);
	}).observe(document, { subtree: true, childList: true, characterData: true,
		attributes: true, attributeFilter: ['aria-busy', 'hidden', 'disabled', 'aria-hidden', 'aria-expanded', 'aria-selected', 'value'] });
	Object.defineProperty(window, '__axscopeMut', { value: rec });
})()`

// installMutations registers the log for every future document and starts it on
// the current one.
func (s *Session) installMutations(sid string) error {
	if _, err := s.client.Send(s.ctx, "Page.addScriptToEvaluateOnNewDocument",
		map[string]any{"source": mutationJS}, sid); err != nil {
		return err
	}
	_, err := s.client.Send(s.ctx, "Runtime.evaluate", map[string]any{"expression": mutationJS}, sid)
	return err
}

// changedSinceJS returns the time (ms since epoch) of the first logged change
// inside the node at or after `since`, 0 when none, -1 when the page has no log.
const changedSinceJS = `function (since) {
	const rec = window.__axscopeMut;
	if (!rec) return -1;
	const root = this.nodeType === 9 ? this.documentElement : this;
	for (const [t, node] of rec.log) {
		if (t >= since && (node === root || root.contains(node))) return t;
	}
	return 0;
}`

// ChangedSince reports when the node (or the document) first changed at or
// after `since`, from the page's mutation log; ok is false when nothing did or
// the page has no log.
func ChangedSince(ctx context.Context, client *cdp.Client, session, objectID string, since time.Time) (time.Time, bool, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": changedSinceJS,
		"arguments":           []any{map[string]any{"value": since.UnixMilli()}},
		"returnByValue":       true,
	}, session)
	if err != nil {
		return time.Time{}, false, err
	}
	var res struct {
		Result struct {
			Value float64 `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return time.Time{}, false, err
	}
	if res.ExceptionDetails != nil {
		return time.Time{}, false, fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	if res.Result.Value <= 0 {
		return time.Time{}, false, nil
	}
	return time.UnixMilli(int64(res.Result.Value)), true, nil
}
