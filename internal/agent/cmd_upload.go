package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// upload sends a file to the page: to the target when given (input or dropzone),
// otherwise to the first `<input type=file>`. The two receive a file differently
// — an input takes the path, a dropzone only understands a drag gesture.
func (a *Agent) upload(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	filePath := req.String("file")
	if filePath == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope upload <file> [target=<ref|text|css=>]"))
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return protocol.Fail(fmt.Errorf("file: %w", err))
	}
	if info.IsDir() {
		return protocol.Fail(fmt.Errorf("%s is a directory, not a file", filePath))
	}

	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	spec := req.String("target")

	var t *browser.Target
	if spec == "" {
		obj, err := browser.FirstFileInput(ctx, a.client(), sid)
		if err != nil {
			return protocol.Fail(err)
		}
		t = &browser.Target{ObjectID: obj}
	} else {
		// A file input takes the file by object, hidden or not; only a dropzone
		// needs geometry, so try the geometry-free resolve first.
		if obj, err := browser.ResolveObject(ctx, a.client(), sid, a.currentRefs(), spec); err == nil && obj != "" {
			if isInput, _ := browser.IsFileInput(ctx, a.client(), sid, obj); isInput {
				t = &browser.Target{ObjectID: obj}
			}
		}
		if t == nil {
			t, sid, err = a.resolve(ctx, sess, spec)
			if err != nil {
				return protocol.Fail(err)
			}
		}
	}

	isInput, err := browser.IsFileInput(ctx, a.client(), sid, t.ObjectID)
	if err != nil {
		return protocol.Fail(err)
	}

	before := a.errCount(sess, sid)
	via := "input"
	inputID := t.ObjectID
	if isInput {
		err = browser.SetFileInput(ctx, a.client(), sid, t.ObjectID, filePath)
	} else {
		// A dropzone is usually a label or box over a hidden <input type=file>;
		// setting the file on that input is what the page listens to. Only a real
		// drop target with no input needs the synthetic drop.
		candidate, cerr := browser.AssociatedFileInput(ctx, a.client(), sid, t.ObjectID)
		if cerr != nil {
			return protocol.Fail(cerr)
		}
		if candidate != "" {
			via = "dropzone→input"
			inputID = candidate
			err = browser.SetFileInput(ctx, a.client(), sid, candidate, filePath)
		} else {
			via = "dropzone"
			err = browser.DropFile(ctx, a.client(), sid, t, filePath, sess.Presenter)
		}
	}
	if err != nil {
		return protocol.Fail(err)
	}

	label := fmt.Sprintf("upload %s [%s]", filepath.Base(filePath), via)
	if spec != "" {
		label = fmt.Sprintf("upload %s on %s [%s]", filepath.Base(filePath), spec, via)
	}
	// `input.files` is not the page's state: a page that reads the file and moves
	// it into its own state leaves the input at 0 (LinkedIn does this once the
	// resume is accepted). Report that as indeterminate instead of a failure — the
	// old wording read as "did not take it" while the page had taken it.
	if count, isInput := browser.FileInputCount(ctx, a.client(), sid, inputID); isInput && count == 0 {
		label += " (the input reports 0 files — the page may have moved the file into its own state; confirm in the UI)"
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}
