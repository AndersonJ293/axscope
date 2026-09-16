// Field actions: fill, type, select and check.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
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

// Select chooses an option in a native <select> (by value or label).
func Select(ctx context.Context, client *cdp.Client, session string, t *Target, want string) error {
	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
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
			return chosen.value;
		}`,
		"arguments":     []map[string]any{{"value": want}},
		"returnByValue": true,
	}, session); err != nil {
		return err
	}
	return nil
}

// SetChecked makes a checkbox/radio reach the wanted state, clicking if needed;
// it reports whether it clicked and the click warning.
func SetChecked(ctx context.Context, client *cdp.Client, session string, t *Target, want bool, p Presenter) (bool, string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": `function () { return !!this.checked; }`,
		"returnByValue":       true,
	}, session)
	if err != nil {
		return false, "", err
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, "", err
	}
	if res.Result.Value == want {
		return false, "", nil
	}
	warning, err := Click(ctx, client, session, t, "left", 1, p)
	return true, warning, err
}
