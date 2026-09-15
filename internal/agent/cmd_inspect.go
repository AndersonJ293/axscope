package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/dom"
	"github.com/ajunior/browser-use/internal/protocol"
)

func (a *Agent) status(ctx context.Context, _ *browser.Session, _ protocol.Request) protocol.Response {
	a.mu.Lock()
	booted := a.sess != nil
	a.mu.Unlock()
	if !booted {
		return ok(fmt.Sprintf("sessão %q: browser ainda não iniciado", a.Session))
	}
	sess := a.mustSess()
	tabs := sess.Tabs()
	sid, _ := sess.ActiveSID()
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	title, _ := dom.EvalString(ctx, a.client(), sid, "document.title")
	var b strings.Builder
	fmt.Fprintf(&b, "sessão: %s\n", a.Session)
	fmt.Fprintf(&b, "url: %s\n", url)
	fmt.Fprintf(&b, "título: %s\n", title)
	fmt.Fprintf(&b, "abas: %d\n", len(tabs))
	fmt.Fprintf(&b, "refs ativas: %d\n", len(a.currentRefs()))
	return ok(strings.TrimRight(b.String(), "\n"))
}

func (a *Agent) snap(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	gen := a.nextGen()
	snap, err := browser.TakeSnapshot(ctx, a.client(), sid, browser.SnapshotOptions{
		RefsOnly: req.Bool("refs", false),
		Tudo:     req.Bool("tudo", false),
		Gen:      gen,
	})
	if err != nil {
		return protocol.Fail(err)
	}
	a.setRefs(snap.Refs, gen)
	sess.UpdateHUD(ctx, "snap")

	var b strings.Builder
	fmt.Fprintf(&b, "título: %s\n", snap.Title)
	fmt.Fprintf(&b, "url: %s\n", snap.URL)
	fmt.Fprintf(&b, "-- %d linhas, %d refs%s\n", snap.Count, len(snap.Refs), linhaDeRolagem(snap))
	b.WriteString(snap.Text)
	if snap.Truncated {
		b.WriteString("\n(... truncado; use `--refs` para reduzir)")
	}
	return ok(b.String())
}

// linhaDeRolagem resume, para o cabeçalho da leitura, onde estão as áreas que
// rolam — quanto já rolou e quanto ainda cabe.
//
// Fica vazia quando não há o que dizer: página que não rola e nenhuma caixa com
// rolagem. Existe porque a árvore de acessibilidade não carrega rolagem: sem
// isso o agente vê as linhas e não sabe onde está nelas.
func linhaDeRolagem(snap *browser.Snapshot) string {
	var partes []string
	if p := snap.Pagina; p != nil && p.Max > 1 {
		partes = append(partes, fmt.Sprintf("%s %d/%d", p.Alvo, p.Pos, p.Max))
	}
	for _, r := range snap.Rolagens {
		if len(partes) >= maxAreasRolagem {
			break
		}
		partes = append(partes, fmt.Sprintf("%s %d/%d", r.Alvo, r.Pos, r.Max))
	}
	if len(partes) == 0 {
		return ""
	}
	linha := " · rolagem: " + strings.Join(partes, " · ")
	if resto := snap.RolagensTotal - len(partes); resto > 0 {
		linha += fmt.Sprintf(" (+%d)", resto)
	}
	return linha
}

// maxAreasRolagem é quantas áreas entram no cabeçalho antes do resumo "+N": o
// cabeçalho é aviso, não inventário.
const maxAreasRolagem = 5

func (a *Agent) read(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	sel := req.String("selector")
	if sel == "" {
		sel = "main, article, [role=main], #content, .content, body"
	}
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s) || document.body;
		return el ? el.innerText : '';
	})()`, strconv.Quote(sel))
	text, err := dom.EvalString(ctx, a.client(), sid, expr)
	if err != nil {
		return protocol.Fail(err)
	}
	text = squeeze(text)
	if len(text) > 8000 {
		text = text[:8000] + "\n(... truncado)"
	}
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	return ok(fmt.Sprintf("url: %s\n\n%s", url, text))
}

func (a *Agent) eval(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	js := req.String("js")
	if js == "" {
		return protocol.Fail(fmt.Errorf("uso: bu eval <js>"))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	raw, err := dom.EvalAwait(ctx, a.client(), sid, js)
	if err != nil {
		return protocol.Fail(err)
	}
	out := string(raw)
	if out == "" {
		out = "undefined"
	}
	return ok(out)
}

func (a *Agent) console(_ context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	level := "error,warn"
	if req.Bool("all", false) {
		level = "all"
	}
	entries := sess.Observe.Console(sid, "", 0)
	var b strings.Builder
	count := 0
	for _, e := range entries {
		if level != "all" && e.Level != "error" && e.Level != "warn" {
			continue
		}
		count++
		loc := ""
		if e.URL != "" {
			loc = fmt.Sprintf(" (%s:%d)", e.URL, e.Line)
		}
		fmt.Fprintf(&b, "[%s] %s%s\n", e.Level, e.Text, loc)
	}
	if count == 0 {
		return ok("(sem erros/avisos de console)")
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}

func (a *Agent) net(_ context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	filter := req.String("filter")
	entries := sess.Observe.Network(sid, filter, 60)
	if len(entries) == 0 {
		return ok("(sem requisições)")
	}
	var b strings.Builder
	for _, e := range entries {
		if e.Failed != "" {
			fmt.Fprintf(&b, "FAIL %s %s (%s)\n", e.Method, e.URL, e.Failed)
			continue
		}
		fmt.Fprintf(&b, "%d %s %s\n", e.Status, e.Method, e.URL)
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}
