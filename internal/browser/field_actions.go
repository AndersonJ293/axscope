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
	if ok, reason := classifyField(describeField(ctx, client, t.ObjSession(session), t.ObjectID)); !ok {
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
	}, t.ObjSession(session)); err != nil {
		return "", err
	}
	if _, err := client.Send(ctx, "Input.insertText", map[string]any{"text": normalizeNewlines(text)}, session); err != nil {
		return "", err
	}
	return fillWarning(text, fieldValue(ctx, client, t.ObjSession(session), t.ObjectID)), nil
}

// Type types character by character (it fires keyboard handlers).
func Type(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) (string, error) {
	if ok, reason := classifyField(describeField(ctx, client, t.ObjSession(session), t.ObjectID)); !ok {
		return "", fmt.Errorf("%s", reason)
	}
	cx, cy := t.actionPoint()
	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.MoveCursor(ctx, client, session, cx, cy)

	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": `function () { if (this.focus) this.focus(); return true; }`,
		"returnByValue":       true,
	}, t.ObjSession(session)); err != nil {
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
	return fillWarning(text, fieldValue(ctx, client, t.ObjSession(session), t.ObjectID)), nil
}

// Select chooses an option in a native <select> or an ARIA combobox/listbox (by
// visible label). It verifies the choice committed and returns a notice when the
// widget ignored it, because a custom widget can highlight an option without
// selecting it.
func Select(ctx context.Context, client *cdp.Client, session string, t *Target, want string, p Presenter) (string, error) {
	obj := t.ObjSession(session)
	if isNativeSelect(ctx, client, obj, t.ObjectID) {
		return selectNative(ctx, client, obj, t.ObjectID, want)
	}
	return selectARIA(ctx, client, session, t, want, p)
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
// The options are looked for in the widget's document: inside a cross-origin
// frame that is the frame's, and the option's box is moved onto the page.
func selectARIA(ctx context.Context, client *cdp.Client, session string, widget *Target, want string, p Presenter) (string, error) {
	obj := widget.ObjSession(session)
	optionID, err := dom.EvalObject(ctx, client, obj, ariaOptionExpression(want))
	if err != nil {
		return "", err
	}
	if optionID == "" {
		return "", fmt.Errorf("no visible option matching %q — open or filter the listbox first (type in the combobox, then select)", want)
	}
	rect, err := widget.PageBox(ctx, client, session, optionID)
	if err != nil {
		return "", fmt.Errorf("the option %q has no visible area: %w", want, err)
	}
	notice, err := Click(ctx, client, session, widget.Sibling(optionID, rect), "left", 1, p)
	if err != nil || notice != "" {
		return notice, err
	}
	if !optionCommitted(ctx, client, obj, optionID) {
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

// SetChecked makes a checkbox, radio or toggle reach the wanted state,
// clicking if needed; it reports whether it clicked and a notice. A custom
// widget is clicked like a native one: refusing it sent the agent to `click`,
// which is what check does anyway.
func SetChecked(ctx context.Context, client *cdp.Client, session string, t *Target, want bool, p Presenter) (bool, string, error) {
	before, what, err := checkedState(ctx, client, session, t)
	if err != nil {
		return false, "", err
	}
	if before != nil && *before == want {
		return false, "", nil
	}
	warning, err := Click(ctx, client, session, t, "left", 1, p)
	if err != nil || warning != "" {
		return true, warning, err
	}
	if before == nil {
		// A toggle drawn with divs keeps its state in a class: there is nothing
		// standard to read, so the click is the whole of `check`.
		return true, fmt.Sprintf("%s has no checked state to read (a custom toggle) — clicked it; the next snap shows whether it took", what), nil
	}
	time.Sleep(40 * time.Millisecond)
	if after, _, err := checkedState(ctx, client, session, t); err == nil && after != nil && *after != want {
		return true, "clicked, but the state did not change — the page may toggle on another element (try `click` on the label or the box)", nil
	}
	return true, "", nil
}

// checkedState reads whether the target is checked, from wherever it says so:
// the element's own `checked`, ARIA (aria-checked/aria-pressed/aria-selected on
// it or its toggle ancestor), the control of a <label>, or the one checkbox or
// radio inside a wrapper. nil means there is no state to read.
func checkedState(ctx context.Context, client *cdp.Client, session string, t *Target) (*bool, string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function () {
			const what = ((this.getAttribute && this.getAttribute('role')) || (this.tagName || '').toLowerCase());
			if (typeof this.checked === 'boolean') return { state: this.checked, what };
			const aria = (el) => {
				for (const a of ['aria-checked', 'aria-pressed', 'aria-selected']) {
					const v = el.getAttribute && el.getAttribute(a);
					if (v === 'true' || v === 'mixed') return true;
					if (v === 'false') return false;
				}
				return null;
			};
			let v = aria(this);
			if (v !== null) return { state: v, what };
			const toggle = this.closest && this.closest('[role=checkbox],[role=switch],[role=radio],[role=menuitemcheckbox],[role=menuitemradio],[role=option],[aria-pressed]');
			if (toggle && toggle !== this && (v = aria(toggle)) !== null) return { state: v, what };
			const label = this.closest && this.closest('label');
			const control = label && label.control;
			if (control && typeof control.checked === 'boolean') return { state: control.checked, what };
			const inner = this.querySelectorAll ? this.querySelectorAll('input[type=checkbox],input[type=radio]') : [];
			if (inner.length === 1) return { state: inner[0].checked, what };
			return { state: null, what };
		}`,
		"returnByValue": true,
	}, t.ObjSession(session))
	if err != nil {
		return nil, "", err
	}
	var res struct {
		Result struct {
			Value struct {
				State *bool  `json:"state"`
				What  string `json:"what"`
			} `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, "", err
	}
	return res.Result.Value.State, res.Result.Value.What, nil
}

// ErrInFrame refuses an action not yet supported on an element of a
// cross-origin iframe, naming the ones that are.
func ErrInFrame(action string) error {
	return fmt.Errorf("%s does not reach inside a cross-origin iframe yet — the actions, wait, find and snap within= do", action)
}
