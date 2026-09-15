// Text field: who accepts what one wants to write, and what to check afterward.
//
// Measured in lab v2: `fill` on a `<select>` answered ok and the value stayed
// the previous one; `fill` on a `<label>` answered ok without having anywhere to
// write. Text that does not go in is worse than an error — the agent goes on as
// if it had filled, and only discovers the opposite when checking the result.
// That is why the refusal states the command that does what was wanted.
package browser

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// fieldDescriptor is what the page says about the target before receiving text.
type fieldDescriptor struct {
	Tag      string `json:"tag"`
	Type     string `json:"type"`
	Editable bool   `json:"editable"`
	Role     string `json:"role"`
}

// describeField asks the page what the target is. It fails silently: without a
// description, the field is treated as accepted — what blocks the wrong one is
// the warning at the end, not a guess of ours.
func describeField(ctx context.Context, client *cdp.Client, session, objectID string) fieldDescriptor {
	var d fieldDescriptor
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			return {
				tag: this.tagName || '',
				type: (this.getAttribute && this.getAttribute('type')) || '',
				editable: !!this.isContentEditable,
				role: (this.getAttribute && this.getAttribute('role')) || ''
			};
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return d
	}
	var res struct {
		Result struct {
			Value fieldDescriptor `json:"value"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &res)
	return res.Result.Value
}

// classifyField says whether the target accepts typed text — and, when it does
// not, the command that does what was wanted.
func classifyField(d fieldDescriptor) (bool, string) {
	tag := strings.ToLower(d.Tag)
	kind := strings.ToLower(d.Type)
	switch {
	case tag == "textarea" || d.Editable:
		return true, ""
	case tag == "select":
		return false, "it is a <select> — to choose the option use `axscope select <target> <value>`"
	case tag == "input":
		switch kind {
		case "checkbox", "radio":
			return false, "it is a checkbox — use `axscope check <target>` (or `uncheck`)"
		case "file":
			return false, "it receives a file — use `axscope upload <file> target=<target>`"
		case "submit", "button", "reset", "image":
			return false, "it is a button — use `axscope click <target>`"
		}
		return true, ""
	case d.Role == "textbox" || d.Role == "searchbox":
		return true, ""
	default:
		return false, "not a text field (it is <" + tag + ">) — text goes into input, textarea or contenteditable"
	}
}

// fillWarning says when the text did not go in.
//
// A masked field changes the value on purpose (the phone becomes "(77) 9 9999…"),
// so a difference is a sign of nothing. Staying empty is: nothing went in.
func fillWarning(sent, value string) string {
	if sent != "" && strings.TrimSpace(value) == "" {
		return "the field is still empty — the text did not go in"
	}
	return ""
}

// normalizeNewlines swaps CRLF for LF: the text comes from wherever it comes
// from (Windows, a file, the agent), and the field has nothing to do with the
// leftover `\r`.
func normalizeNewlines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// keyFor returns the named key that represents the character — or "", when the
// character goes as text itself.
//
// It is only the line break, and it matters: sending `\n` as text inserts
// nothing, so the character vanished silently when typing character by character
// — measured in lab v3, `alfa\nbeta` became `alfabeta`.
func keyFor(r rune) string {
	if r == '\n' || r == '\r' {
		return "Enter"
	}
	return ""
}

// fieldValue reads what the field has after filling.
func fieldValue(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (this.isContentEditable) return this.innerText || '';
			if ('value' in this) return String(this.value || '');
			return this.innerText || '';
		}`,
		"returnByValue": true,
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
