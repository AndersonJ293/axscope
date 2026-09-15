package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// upload sends a file to the page.
//
// Two paths, because the web receives a file in two ways: the `<input
// type=file>` — a form path, almost always hidden behind a button — and the
// dropzone, which only understands dragging. The target decides which; without a
// target the first `<input type=file>` holds, which is the guess that usually
// gets it right.
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

	var (
		t   *browser.Target
		sid string
	)
	spec := req.String("target")
	if spec != "" {
		t, sid, err = a.resolve(ctx, sess, spec)
		if err != nil {
			return protocol.Fail(err)
		}
	} else {
		sid, err = a.activeSID(sess)
		if err != nil {
			return protocol.Fail(err)
		}
		obj, err := browser.FirstFileInput(ctx, a.client(), sid)
		if err != nil {
			return protocol.Fail(err)
		}
		t = &browser.Target{ObjectID: obj}
	}
	isInput, err := browser.IsFileInput(ctx, a.client(), sid, t.ObjectID)
	if err != nil {
		return protocol.Fail(err)
	}

	before := a.errCount(sess, sid)
	via := "input"
	if isInput {
		err = browser.SetFileInput(ctx, a.client(), sid, t.ObjectID, filePath)
	} else {
		via = "dropzone"
		err = browser.DropFile(ctx, a.client(), sid, t, filePath, sess.Presenter)
	}
	if err != nil {
		return protocol.Fail(err)
	}

	label := fmt.Sprintf("upload %s [%s]", filepath.Base(filePath), via)
	if spec != "" {
		label = fmt.Sprintf("upload %s on %s [%s]", filepath.Base(filePath), spec, via)
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}
