// Field actions: fill, type, select and check.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// Fill replaces the field's content (focus + selection + insertText).
func Fill(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) (string, error) {
	if ok, reason := classifyField(describeField(ctx, client, session, t.ObjectID)); !ok {
		return "", fmt.Errorf("%s", reason)
	}
	cx, cy := t.actionPoint()
	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.MoveCursor(ctx, client, session, cx, cy)

	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function () {
			if (this.focus) this.focus();
			if (this.select) { this.select(); }
			else {
				const range = document.createRange();
				range.selectNodeContents(this);
				const sel = getSelection();
				sel.removeAllRanges();
				sel.addRange(range);
			}
			return true;
		}`,
		"returnByValue": true,
	}, session); err != nil {
		return "", err
	}
	if d := visualDelay(); d > 0 {
		time.Sleep(d / 2)
	}
	if _, err := client.Send(ctx, "Input.insertText", map[string]any{"text": normalizeNewlines(text)}, session); err != nil {
		return "", err
	}
	return fillWarning(text, fieldValue(ctx, client, session, t.ObjectID)), nil
}

// Type types character by character (it fires keyboard handlers).
func Type(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) (string, error) {
	if ok, reason := classifyField(describeField(ctx, client, session, t.ObjectID)); !ok {
		return "", fmt.Errorf("%s", reason)
	}
	cx, cy := t.actionPoint()
	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.MoveCursor(ctx, client, session, cx, cy)

	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": `function () { if (this.focus) this.focus(); return true; }`,
		"returnByValue":       true,
	}, session); err != nil {
		return "", err
	}
	for _, r := range normalizeNewlines(text) {
		if key := keyFor(r); key != "" {
			if err := Press(ctx, client, session, key); err != nil {
				return "", err
			}
			time.Sleep(8 * time.Millisecond)
			continue
		}
		s := string(r)
		if _, err := client.Send(ctx, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyDown", "text": s,
		}, session); err != nil {
			return "", err
		}
		if _, err := client.Send(ctx, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyUp",
		}, session); err != nil {
			return "", err
		}
		time.Sleep(8 * time.Millisecond)
	}
	return fillWarning(text, fieldValue(ctx, client, session, t.ObjectID)), nil
}

// Select chooses an option in a native <select> or an ARIA combobox/listbox (by
// visible label). It verifies the choice committed and returns a notice when the
// widget ignored it, because a custom widget can highlight an option without
// selecting it.
func Select(ctx context.Context, client *cdp.Client, session string, t *Target, want string, p Presenter) (string, error) {
	if isNativeSelect(ctx, client, session, t.ObjectID) {
		return selectNative(ctx, client, session, t.ObjectID, want)
	}
	return selectARIA(ctx, client, session, want, p)
}

// isNativeSelect tells a real <select> from an ARIA widget.
func isNativeSelect(ctx context.Context, client *cdp.Client, session, objectID string) bool {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": `function () { return (this.tagName || '').toLowerCase() === 'select'; }`,
		"returnByValue":       true,
	}, session)
	if err != nil {
		return false
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	return json.Unmarshal(raw, &res) == nil && res.Result.Value
}

// selectNative sets the option and dispatches the events the page listens to,
// then checks the option stayed selected (a change handler can revert it).
func selectNative(ctx context.Context, client *cdp.Client, session, objectID, want string) (string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function (want) {
			const norm = (s) => (s || '').trim();
			let chosen = null;
			for (const opt of this.options || []) {
				if (opt.value === want || norm(opt.label) === want || norm(opt.textContent) === want) {
					chosen = opt; break;
				}
			}
			if (!chosen) throw new Error('option not found: ' + want);
			chosen.selected = true;
			this.dispatchEvent(new Event('input', { bubbles: true }));
			this.dispatchEvent(new Event('change', { bubbles: true }));
			return { value: String(this.value), committed: chosen.selected === true };
		}`,
		"arguments":     []map[string]any{{"value": want}},
		"returnByValue": true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value struct {
				Value     string `json:"value"`
				Committed bool   `json:"committed"`
			} `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	if !res.Result.Value.Committed {
		return fmt.Sprintf("the selection did not commit — <select> kept %q", res.Result.Value.Value), nil
	}
	return "", nil
}

// selectARIA clicks the matching role=option with the real pointer (the same
// path as `click`) and then checks the widget committed the choice.
func selectARIA(ctx context.Context, client *cdp.Client, session, want string, p Presenter) (string, error) {
	optionID, err := dom.EvalObject(ctx, client, session, ariaOptionExpression(want))
	if err != nil {
		return "", err
	}
	if optionID == "" {
		return "", fmt.Errorf("no visible option matching %q — open or filter the listbox first (type in the combobox, then select)", want)
	}
	rect, err := dom.BoxOf(ctx, client, session, optionID)
	if err != nil {
		return "", fmt.Errorf("the option %q has no visible area: %w", want, err)
	}
	notice, err := Click(ctx, client, session, &Target{ObjectID: optionID, Rect: rect}, "left", 1, p)
	if err != nil || notice != "" {
		return notice, err
	}
	if !optionCommitted(ctx, client, session, optionID) {
		return "the selection did not commit — the option was highlighted but not selected (a custom combobox? try `type` then `press Enter`, or `eval`)", nil
	}
	return "", nil
}

// optionCommitted reports the standard signals that an ARIA option became the
// selection: aria-selected on it, or it is the combobox's active descendant. An
// unreadable answer counts as committed, so a working widget is not warned about.
func optionCommitted(ctx context.Context, client *cdp.Client, session, objectID string) bool {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (this.getAttribute('aria-selected') === 'true') return true;
			if (this.getAttribute('data-selected') === 'true') return true;
			const widget = this.closest && this.closest('[role=combobox]');
			if (widget) {
				const active = widget.getAttribute('aria-activedescendant') || '';
				if (active && this.id && active === this.id) return true;
			}
			return false;
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return true
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return true
	}
	return res.Result.Value
}

// ariaOptionExpression returns the visible role=option element whose label best
// matches `want`, sharing the naming helper with the text target so the two agree.
func ariaOptionExpression(want string) string {
	return fmt.Sprintf(`(() => {
		const want = %s;
		%s
		%s
		const nodes = underShadow(document, '[role=option]', []);
		const betterThan = (a, b) => a[0] - b[0] || a[1] - b[1];
		let best = null, key = null;
		for (const el of nodes) {
			if (hidden(el)) continue;
			const t = text(el);
			if (!t) continue;
			const exact = t === want;
			if (!exact && !t.includes(want)) continue;
			const current = [exact ? 0 : 1, t.length - want.length];
			if (key === null || betterThan(current, key) < 0) { best = el; key = current; }
		}
		return best;
	})()`, strconv.Quote(want), underShadow, jsElementText)
}

// SetChecked makes a checkbox/radio reach the wanted state, clicking if needed;
// it reports whether it clicked and the click warning.
func SetChecked(ctx context.Context, client *cdp.Client, session string, t *Target, want bool, p Presenter) (bool, string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function () {
			return {
				kind: typeof this.checked,
				current: !!this.checked,
				role: (this.getAttribute && this.getAttribute('role')) || '',
				tag: (this.tagName || '').toLowerCase()
			};
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return false, "", err
	}
	var res struct {
		Result struct {
			Value struct {
				Kind    string `json:"kind"`
				Current bool   `json:"current"`
				Role    string `json:"role"`
				Tag     string `json:"tag"`
			} `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, "", err
	}
	if v := res.Result.Value; v.Kind != "boolean" {
		what := v.Tag
		if v.Role != "" {
			what = v.Role
		}
		return false, "", fmt.Errorf("not a checkbox or radio (it is %s) — for a listbox option use `axscope select`, for a toggle `axscope click`", what)
	}
	if res.Result.Value.Current == want {
		return false, "", nil
	}
	warning, err := Click(ctx, client, session, t, "left", 1, p)
	return true, warning, err
}
