package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/protocol"
)

func (a *Agent) tabs(_ context.Context, sess *browser.Session, _ protocol.Request) protocol.Response {
	tabs := sess.Tabs()
	if len(tabs) == 0 {
		return ok("(nenhuma aba)")
	}
	var b strings.Builder
	for _, t := range tabs {
		marker := " "
		if t.Active {
			marker = "*"
		}
		title := t.Title
		if title == "" {
			title = "(sem título)"
		}
		fmt.Fprintf(&b, "%s[%d] %s — %s\n", marker, t.Index, title, t.URL)
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}

func (a *Agent) switchTab(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	ref := req.String("ref")
	if ref == "" {
		return protocol.Fail(fmt.Errorf("uso: bu tab <índice|targetId>"))
	}
	tab, err := sess.Select(ctx, ref, req.Bool("focus", false))
	if err != nil {
		return protocol.Fail(err)
	}
	sess.UpdateHUD(ctx, "tab "+ref)
	return ok(fmt.Sprintf("ok: aba %s — %s", tab.TargetID, tab.URL))
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
	return ok(fmt.Sprintf("ok: nova aba %s — %s", tab.TargetID, url))
}

func (a *Agent) closeTab(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	ref := req.String("ref")
	if ref == "" {
		return protocol.Fail(fmt.Errorf("uso: bu closetab <índice|targetId>"))
	}
	if err := sess.CloseTab(ctx, ref); err != nil {
		return protocol.Fail(err)
	}
	return ok("ok: aba fechada")
}
