package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

func (a *Agent) tabs(_ context.Context, sess *browser.Session, _ protocol.Request) protocol.Response {
	tabs := sess.Tabs()
	if len(tabs) == 0 {
		return ok("(no tabs)")
	}
	var b strings.Builder
	for _, t := range tabs {
		marker := " "
		if t.Active {
			marker = "*"
		}
		title := t.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "%s[%d] %s — %s\n", marker, t.Index, title, t.URL)
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}

func (a *Agent) switchTab(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	ref := req.String("ref")
	if ref == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope tab <index|targetId>"))
	}
	tab, err := sess.Select(ctx, ref, req.Bool("focus", false))
	if err != nil {
		return protocol.Fail(err)
	}
	sess.UpdateHUD(ctx, "tab "+ref)
	return ok(fmt.Sprintf("ok: tab %s — %s", tab.TargetID, tab.URL))
}

func (a *Agent) newTab(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	url := req.String("url")
	if url == "" {
		url = "about:blank"
	}
	tab, err := sess.NewTab(ctx, url)
	if err != nil {
		return protocol.Fail(err)
	}
	sess.UpdateHUD(ctx, "newtab")
	return ok(fmt.Sprintf("ok: new tab %s — %s", tab.TargetID, url))
}

func (a *Agent) closeTab(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	ref := req.String("ref")
	if ref == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope closetab <index|targetId>"))
	}
	if err := sess.CloseTab(ctx, ref); err != nil {
		return protocol.Fail(err)
	}
	return ok("ok: tab closed")
}
