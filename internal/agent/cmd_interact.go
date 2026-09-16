package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/dom"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

func (a *Agent) clickLike(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	if target == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope %s <target>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if req.Cmd == "hover" {
		if err := browser.Hover(ctx, a.client(), sid, t, sess.Presenter); err != nil {
			return protocol.Fail(err)
		}
		return ok(a.finish(ctx, sess, sid, "hover "+target, before))
	}
	button := "left"
	if req.Bool("right", false) {
		button = "right"
	} else if req.Bool("middle", false) {
		button = "middle"
	}
	count := 1
	if req.Bool("double", false) {
		count = 2
	}
	action := "click"
	if count == 2 {
		action = "dblclick"
	}
	notice, err := browser.Click(ctx, a.client(), sid, t, button, count, sess.Presenter)
	if err != nil {
		return protocol.Fail(actionFailure(action, target, err))
	}
	return ok(a.finish(ctx, sess, sid, withNotice(action+" "+target, notice), before))
}

func (a *Agent) drag(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	fromSpec := req.String("from")
	toSpec := req.String("to")
	if fromSpec == "" || toSpec == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope drag <from> <to>"))
	}
	from, sid, err := a.resolve(ctx, sess, fromSpec)
	if err != nil {
		return protocol.Fail(err)
	}
	to, _, err := a.resolve(ctx, sess, toSpec)
	if err != nil {
		return protocol.Fail(err)
	}
	at := ""
	if req.Bool("top", false) {
		at = "top"
	} else if req.Bool("bottom", false) {
		at = "bottom"
	}
	before := a.errCount(sess, sid)
	kind, changed, err := browser.Drag(ctx, a.client(), sid, from, to, browser.DragOptions{
		DropAt: at,
		Type:   req.String("type"),
	}, sess.Presenter)
	if err != nil {
		return protocol.Fail(err)
	}
	label := fmt.Sprintf("drag %s -> %s [%s]", fromSpec, toSpec, kind)
	if !changed {
		// A gesture that does not catch may have ended as a real click on the target.
		label += " (saw no position change — it may not have caught)"
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}

func (a *Agent) fillLike(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	// The argument is `value`, not `text`: `text=` is a target selector and would
	// otherwise be swallowed as a key=value pair, making the target the content.
	text := req.String("value")
	if target == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope %s <target> <value>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	var notice string
	if req.Cmd == "fill" {
		notice, err = browser.Fill(ctx, a.client(), sid, t, text, sess.Presenter)
	} else {
		notice, err = browser.Type(ctx, a.client(), sid, t, text, sess.Presenter)
	}
	if err != nil {
		return protocol.Fail(actionFailure(req.Cmd, target, err))
	}
	label := fmt.Sprintf("%s %s = %s", req.Cmd, target, strconv.Quote(text))
	if notice != "" {
		label += " (" + notice + ")"
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}

func (a *Agent) press(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	key := req.String("key")
	if key == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope press <key>"))
	}
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if err := browser.Press(ctx, a.client(), sid, key); err != nil {
		return protocol.Fail(err)
	}
	return ok(a.finish(ctx, sess, sid, "press "+key, before))
}

func (a *Agent) selectOption(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	value := req.String("value")
	if target == "" || value == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope select <target> <value>"))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if err := browser.Select(ctx, a.client(), sid, t, value); err != nil {
		return protocol.Fail(err)
	}
	return ok(a.finish(ctx, sess, sid, fmt.Sprintf("select %s = %s", target, value), before))
}

func (a *Agent) checkLike(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	if target == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope %s <target>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	want := req.Cmd == "check"
	clicked, notice, err := browser.SetChecked(ctx, a.client(), sid, t, want, sess.Presenter)
	if err != nil {
		return protocol.Fail(actionFailure(req.Cmd, target, err))
	}
	label := req.Cmd + " " + target
	if !clicked {
		label += " (was already)"
	}
	return ok(a.finish(ctx, sess, sid, withNotice(label, notice), before))
}

// withNotice appends what the action could not do — the click that was sent and
// did not reach the target.
func withNotice(label, notice string) string {
	if notice == "" {
		return label
	}
	return label + " (" + notice + ")"
}

// actionFailure wraps the reason an action was not sent; the refusal names the
// next step (wait for enable, remove the cover, or click by point with `pos=x,y`).
func actionFailure(action, target string, err error) error {
	return fmt.Errorf("%s on %s was not sent: %w", action, target, err)
}

// scroll scrolls whatever is under the center of the screen, or the container
// of `target` — a box that scrolls inside the page is not scrolled by scrolling
// the page, so the target is the only way to say which box to move.
func (a *Agent) scroll(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	raw := req.String("dy")
	if raw == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope scroll <dy> [target=<ref|text|css>] (positive dy goes down)"))
	}
	dy, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return protocol.Fail(fmt.Errorf("invalid dy: %q", raw))
	}
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}

	if target := req.String("target"); target != "" {
		t, _, err := a.resolve(ctx, sess, target)
		if err != nil {
			return protocol.Fail(err)
		}
		where, err := browser.ScrollTarget(ctx, a.client(), sid, t.ObjectID, 0, dy)
		if err != nil {
			return protocol.Fail(err)
		}
		sess.Settle(ctx, sid, actionIdle)
		label := fmt.Sprintf("scroll %.0f in %s — now at %s", dy, target, where)
		sess.UpdateHUD(ctx, label)
		return ok("ok: " + label)
	}

	where, err := browser.Scroll(ctx, a.client(), sid, 0, dy, req.Bool("page", false))
	if err != nil {
		return protocol.Fail(err)
	}
	sess.Settle(ctx, sid, actionIdle)
	label := fmt.Sprintf("scroll %.0f — now at %s", dy, where)
	sess.UpdateHUD(ctx, label)
	return ok("ok: " + label)
}

// finish summarizes the result of an action and attaches console warnings.
func (a *Agent) finish(ctx context.Context, sess *browser.Session, sid, label string, errCountBefore int) string {
	sess.Settle(ctx, sid, actionIdle)
	sess.UpdateHUD(ctx, label)
	var b strings.Builder
	fmt.Fprintf(&b, "ok: %s", label)
	newErrs := sess.Observe.Console(sid, "error", 0)
	if len(newErrs) > errCountBefore {
		for _, e := range newErrs[errCountBefore:] {
			fmt.Fprintf(&b, "\n!! console: %s", e.Text)
		}
	}
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	fmt.Fprintf(&b, "\nurl: %s", url)
	return b.String()
}

func (a *Agent) errCount(sess *browser.Session, sid string) int {
	return len(sess.Observe.Console(sid, "error", 0))
}
