// File upload: `<input type=file>` via DOM.setFileInputFiles, or a dropzone that
// takes an in-page File in a DataTransfer (`items.add`, since `files` is read-only).
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// FirstFileInput returns the first `<input type=file>` on the page, the target the
// browser accepts without a dialog when none is given.
func FirstFileInput(ctx context.Context, client *cdp.Client, session string) (string, error) {
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "document.querySelector('input[type=file]')",
		"returnByValue": false,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.ObjectID == "" {
		return "", fmt.Errorf("could not find <input type=file> on the page — pass target=<ref|css=|text=>")
	}
	return res.Result.ObjectID, nil
}

// IsFileInput says whether the element is an `<input type=file>` or a dropzone.
func IsFileInput(ctx context.Context, client *cdp.Client, session, objectID string) (bool, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			return !!(this && this.tagName === 'INPUT' && this.type === 'file');
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return false, err
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return false, fmt.Errorf("could not read the target")
	}
	return res.Result.Value, nil
}

// SetFileInput delivers `path` to the `<input type=file>`. The browser reads the
// path, so it only holds when both are on the same machine; otherwise the
// dropzone is the path, which travels in bytes.
func SetFileInput(ctx context.Context, client *cdp.Client, session, objectID, path string) error {
	_, err := client.Send(ctx, "DOM.setFileInputFiles", map[string]any{
		"files":    []string{path},
		"objectId": objectID,
	}, session)
	return err
}

// AssociatedFileInput returns the object id of the `<input type=file>` a dropzone
// or label stands for — its own subtree, the label's control, or the page's first
// one — or "" when there is none. Setting the file on that input is what the page
// listens to, and it works while the input is hidden.
func AssociatedFileInput(ctx context.Context, client *cdp.Client, session, objectID string) (string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			const pick = (root) => (root && root.querySelector) ? root.querySelector('input[type=file]') : null;
			let input = pick(this);
			if (!input && this.closest) {
				const label = this.closest('label');
				if (label && label.control && label.control.type === 'file') input = label.control;
			}
			if (!input) input = document.querySelector('input[type=file]');
			return input || null;
		}`,
		"returnByValue": false,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return "", nil
	}
	return res.Result.ObjectID, nil
}

// FileInputCount returns how many files an `<input type=file>` holds and whether
// it is one at all, so a drop that attached nothing is not answered with `ok`.
func FileInputCount(ctx context.Context, client *cdp.Client, session, objectID string) (int, bool) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (!this || this.tagName !== 'INPUT' || this.type !== 'file') return { input: false, count: 0 };
			return { input: true, count: this.files ? this.files.length : 0 };
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return 0, false
	}
	var res struct {
		Result struct {
			Value struct {
				Input bool `json:"input"`
				Count int  `json:"count"`
			} `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return 0, false
	}
	return res.Result.Value.Count, res.Result.Value.Input
}

// DropFile emits a file drag over the target with the real content inside the DataTransfer.
func DropFile(ctx context.Context, client *cdp.Client, session string, t *Target, path string, p Presenter) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	name := filepath.Base(path)
	kind := mime.TypeByExtension(filepath.Ext(name))
	if kind == "" {
		kind = "application/octet-stream"
	}

	x, y := t.actionPoint()
	if t.ObjectID != "" {
		_ = p.Spotlight(ctx, client, session, &t.Rect)
		_ = p.MoveCursor(ctx, client, session, x, y)
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": dropFileScript,
		"arguments": []any{
			map[string]any{"value": name},
			map[string]any{"value": kind},
			map[string]any{"value": b64},
			map[string]any{"value": x},
			map[string]any{"value": y},
		},
		"returnByValue": true,
	}, session)
	if err != nil {
		return err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		return fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	if res.Result.Value != "ok" {
		return fmt.Errorf("could not drop the file: %s", res.Result.Value)
	}
	return nil
}

// dropFileScript builds the file inside the page and emits the drag. The order
// matters: dragenter warns, dragover is where the dropzone calls preventDefault,
// and only then the drop delivers.
const dropFileScript = `function (name, kind, b64, x, y) {
	const bin = atob(b64);
	const bytes = new Uint8Array(bin.length);
	for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
	const dt = new DataTransfer();
	dt.items.add(new File([bytes], name, { type: kind }));
	const over = document.elementFromPoint(x, y) || this;
	if (!over) return 'no target under the point';
	const fire = (event) => over.dispatchEvent(new DragEvent(event, {
		bubbles: true, cancelable: true, composed: true, dataTransfer: dt,
		clientX: x, clientY: y, screenX: x, screenY: y,
	}));
	fire('dragenter');
	fire('dragover');
	fire('drop');
	fire('dragend');
	return 'ok';
}`
