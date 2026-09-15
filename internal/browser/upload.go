// File upload: the two paths the web uses to receive one.
//
// A `<input type=file>` the browser fills on the user's behalf
// (`DOM.setFileInputFiles` fires input/change as if the file had been chosen).
// It is the form path, and the input is usually hidden behind a button —
// invisible in the reading, and still the right target.
//
// A dropzone only understands a file coming from a drag: there the content
// becomes a `File` inside the page, inside a real `DataTransfer`, and the drop is
// emitted over the target. `DataTransfer.files` is read-only by assignment, but
// `items.add(File)` populates it — that is what brought down the assumption that
// a file cannot be forged in JavaScript.
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

// FirstFileInput returns the first `<input type=file>` on the page.
//
// It exists because, without a target, it is the one wanted: it is usually
// hidden, and it is the path the browser accepts without any dialog.
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

// IsFileInput says whether the element is an `<input type=file>` (it accepts the
// file directly) or anything else (it only accepts by drag).
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

// SetFileInput delivers `path` to the `<input type=file>`.
//
// The path is read by the browser, not by us: it holds when the two are on the
// same machine (the extension case). Outside that, the path does not exist on the
// other side — there the path is the dropzone, which travels in bytes.
func SetFileInput(ctx context.Context, client *cdp.Client, session, objectID, path string) error {
	_, err := client.Send(ctx, "DOM.setFileInputFiles", map[string]any{
		"files":    []string{path},
		"objectId": objectID,
	}, session)
	return err
}

// DropFile emits a file drag over the target, with the real content inside the
// DataTransfer.
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

// dropFileScript builds the file inside the page and emits the drag.
//
// The order matters to whoever listens: dragenter warns that something arrived,
// dragover is usually where the dropzone marks itself as a target (and where it
// calls preventDefault to allow the drop), and only then the drop delivers.
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
