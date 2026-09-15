package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/dom"
	"github.com/ajunior/browser-use/internal/protocol"
)

func (a *Agent) open(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	url := req.String("url")
	if url == "" {
		return protocol.Fail(fmt.Errorf("uso: bu open <url> [--new]"))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	if req.Bool("new", false) {
		tab, err := sess.NewTab(ctx, url)
		if err != nil {
			return protocol.Fail(err)
		}
		sid = tab.SessionID
	} else if err := sess.Navigate(ctx, sid, url, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	sess.UpdateHUD(ctx, "open "+url)
	title, _ := dom.EvalString(ctx, a.client(), sid, "document.title")
	return ok(fmt.Sprintf("ok: %s\n%s", url, title))
}

func (a *Agent) wait(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	want := req.String("text")
	if want == "" {
		return protocol.Fail(fmt.Errorf("uso: bu %s <texto>", req.Cmd))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	timeout := navTimeout
	if v := req.Int("timeout", 0); v > 0 {
		timeout = time.Duration(v) * time.Millisecond
	}
	present := req.Cmd == "wait"
	start := time.Now()
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		has, err := pageHasText(ctx, a.client(), sid, want)
		if err == nil && has == present {
			verb := "apareceu"
			if !present {
				verb = "sumiu"
			}
			return ok(fmt.Sprintf("ok: %q %s em %dms", want, verb, time.Since(start).Milliseconds()))
		}
		time.Sleep(120 * time.Millisecond)
	}
	if present {
		return protocol.Fail(fmt.Errorf("%q não apareceu em %s", want, timeout))
	}
	return protocol.Fail(fmt.Errorf("%q não sumiu em %s", want, timeout))
}

func (a *Agent) history(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	delta := -1
	if req.Cmd == "forward" {
		delta = 1
	}
	if err := sess.HistoryMove(ctx, sid, delta, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	return ok(fmt.Sprintf("ok: %s\nurl: %s", req.Cmd, url))
}

func (a *Agent) reload(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	if err := sess.Reload(ctx, sid, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	return ok("ok: recarreguei")
}

// pageHasText diz se o corpo da página contém o texto pedido.
func pageHasText(ctx context.Context, client *cdp.Client, session, want string) (bool, error) {
	expr := fmt.Sprintf(`(() => {
		const body = document.body;
		if (!body) return false;
		return (body.innerText || '').includes(%s);
	})()`, strconv.Quote(want))
	raw, err := dom.Eval(ctx, client, session, expr)
	if err != nil {
		return false, err
	}
	if len(raw) == 0 {
		return false, nil
	}
	var has bool
	if err := json.Unmarshal(raw, &has); err != nil {
		return false, err
	}
	return has, nil
}
