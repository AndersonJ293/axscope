// Agente: executa os comandos sobre a sessão do browser e devolve texto para o
// agente de IA ler. É o mesmo despachante para CLI, daemon (roteiro) e MCP.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ajunior/browser-use/internal/bridge"
	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/command"
	"github.com/ajunior/browser-use/internal/protocol"
)

const (
	actionIdle    = 300 * time.Millisecond
	actionTimeout = 8 * time.Second
	navTimeout    = 45 * time.Second
)

// Agent mantém o browser e o estado entre comandos.
type Agent struct {
	Session  string
	Attach   string
	Headless bool
	Engine   string

	runMu sync.Mutex

	mu             sync.Mutex
	handle         *browser.Handle
	sess           *browser.Session
	refs           map[string]int
	overlayVisible bool
	bridge         *bridge.Server
	extClient      *cdp.Client
}

// Close encerra o browser (se fomos nós que subimos) e a conexão.
func (a *Agent) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handle != nil {
		a.handle.Client.Close()
		if !a.handle.Attached {
			a.handle.Kill()
		}
	}
	a.handle = nil
	a.sess = nil
}

// degraded diz se o browser que temos não serve mais (morreu ou caiu a conexão).
// Sem isso, um browser morto deixaria o daemon vivo respondendo erro para sempre.
func (a *Agent) degraded() bool {
	if a.handle == nil || a.sess == nil {
		return true
	}
	if a.handle.Client.Err() != nil {
		return true
	}
	if a.handle.Exited() {
		return true
	}
	return false
}

// extension sobe a ponte (uma vez) e espera a extensão conectar.
func (a *Agent) extension(ctx context.Context) (*cdp.Client, error) {
	if a.bridge == nil {
		port := 0
		if v := os.Getenv("BROWSER_USE_BRIDGE_PORT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				port = n
			}
		}
		srv, err := bridge.Start(a.Session, port)
		if err != nil {
			return nil, err
		}
		a.bridge = srv
	}
	if a.extClient != nil && a.extClient.Err() == nil {
		return a.extClient, nil
	}
	client, err := a.bridge.Wait(ctx, 20*time.Second)
	if err != nil {
		return nil, err
	}
	a.extClient = client
	return client, nil
}

func (a *Agent) ensure(ctx context.Context) (*browser.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.degraded() {
		return a.sess, nil
	}
	// Descarta o que morreu antes de subir de novo.
	if a.handle != nil {
		a.handle.Client.Close()
		if !a.handle.Attached && !a.handle.Exited() {
			a.handle.Kill()
		}
		a.handle = nil
		a.sess = nil
		a.refs = nil
	}
	var handle *browser.Handle
	var err error
	switch {
	case a.Attach != "":
		handle, err = browser.Attach(ctx, a.Attach)
	case a.Engine == browser.EngineExt:
		// Não subimos browser nenhum: a extensão no Brave se conecta até nós.
		var client *cdp.Client
		client, err = a.extension(ctx)
		if err == nil {
			handle = &browser.Handle{Client: client, Executable: "(extensão)", Attached: true}
		}
	default:
		handle, err = browser.Launch(ctx, browser.LaunchOptions{
			Session:  a.Session,
			Engine:   a.Engine,
			Headless: a.Headless,
		})
	}
	if err != nil {
		return nil, err
	}
	sess, err := browser.NewSession(ctx, handle.Client, false)
	if err != nil {
		handle.Client.Close()
		return nil, err
	}
	a.handle = handle
	a.sess = sess
	a.overlayVisible = true
	return sess, nil
}

func (a *Agent) client() *cdp.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handle == nil {
		return nil
	}
	return a.handle.Client
}

// Run executa um pedido. Serializa tudo para não misturar ações.
func (a *Agent) Run(ctx context.Context, req protocol.Request) protocol.Response {
	a.runMu.Lock()
	defer a.runMu.Unlock()

	if req.Cmd == "script" {
		return a.runScript(ctx, req)
	}
	return a.dispatch(ctx, req)
}

// Ping responde sem subir browser.
func (a *Agent) Ping() bool { return true }

func (a *Agent) dispatch(ctx context.Context, req protocol.Request) protocol.Response {
	switch req.Cmd {
	case "ping":
		return ok("pong")
	case "status":
		return a.status(ctx)
	}

	sess, err := a.ensure(ctx)
	if err != nil {
		return protocol.Fail(err)
	}

	switch req.Cmd {
	case "open":
		return a.open(ctx, sess, req)
	case "snap":
		return a.snap(ctx, sess, req)
	case "click", "hover":
		return a.clickLike(ctx, sess, req)
	case "fill", "type":
		return a.fillLike(ctx, sess, req)
	case "press":
		return a.press(ctx, sess, req)
	case "select":
		return a.selectOption(ctx, sess, req)
	case "check", "uncheck":
		return a.checkLike(ctx, sess, req)
	case "scroll":
		return a.scroll(ctx, sess, req)
	case "wait", "waitgone":
		return a.wait(ctx, sess, req)
	case "read":
		return a.read(ctx, sess, req)
	case "eval":
		return a.eval(ctx, sess, req)
	case "tabs":
		return a.tabs(sess)
	case "tab":
		return a.switchTab(ctx, sess, req)
	case "newtab":
		return a.newTab(ctx, sess, req)
	case "closetab":
		return a.closeTab(ctx, sess, req)
	case "back", "forward":
		return a.history(ctx, sess, req)
	case "reload":
		return a.reload(ctx, sess, req)
	case "console":
		return a.console(sess, req)
	case "net":
		return a.net(sess, req)
	case "shot":
		return a.shot(ctx, sess, req)
	default:
		return protocol.Fail(fmt.Errorf("comando %q não é tratado pelo daemon", req.Cmd))
	}
}

// ---- helpers de sessão ----

func (a *Agent) activeSID(sess *browser.Session) (string, error) {
	return sess.ActiveSID()
}

func (a *Agent) setRefs(refs map[string]int) {
	a.mu.Lock()
	a.refs = refs
	a.mu.Unlock()
}

func (a *Agent) currentRefs() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refs
}

// ---- comandos ----

func (a *Agent) status(ctx context.Context) protocol.Response {
	a.mu.Lock()
	booted := a.sess != nil
	a.mu.Unlock()
	if !booted {
		return ok(fmt.Sprintf("sessão %q: browser ainda não iniciado", a.Session))
	}
	sess := a.mustSess()
	tabs := sess.Tabs()
	sid, _ := sess.ActiveSID()
	url, _ := evalString(ctx, a.client(), sid, "location.href")
	title, _ := evalString(ctx, a.client(), sid, "document.title")
	var b strings.Builder
	fmt.Fprintf(&b, "sessão: %s\n", a.Session)
	fmt.Fprintf(&b, "url: %s\n", url)
	fmt.Fprintf(&b, "título: %s\n", title)
	fmt.Fprintf(&b, "abas: %d\n", len(tabs))
	fmt.Fprintf(&b, "refs ativas: %d\n", len(a.currentRefs()))
	return ok(strings.TrimRight(b.String(), "\n"))
}

func (a *Agent) mustSess() *browser.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sess
}

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
	title, _ := evalString(ctx, a.client(), sid, "document.title")
	return ok(fmt.Sprintf("ok: %s\n%s", url, title))
}

func (a *Agent) snap(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	snap, err := browser.TakeSnapshot(ctx, a.client(), sid, browser.SnapshotOptions{
		RefsOnly: req.Bool("refs", false),
	})
	if err != nil {
		return protocol.Fail(err)
	}
	a.setRefs(snap.Refs)
	sess.UpdateHUD(ctx, "snap")

	var b strings.Builder
	fmt.Fprintf(&b, "título: %s\n", snap.Title)
	fmt.Fprintf(&b, "url: %s\n", snap.URL)
	fmt.Fprintf(&b, "-- %d linhas, %d refs\n", snap.Count, len(snap.Refs))
	b.WriteString(snap.Text)
	if snap.Truncated {
		b.WriteString("\n(... truncado; use `--refs` para reduzir)")
	}
	return ok(b.String())
}

func (a *Agent) resolve(ctx context.Context, sess *browser.Session, target string) (*browser.Target, string, error) {
	sid, err := a.activeSID(sess)
	if err != nil {
		return nil, "", err
	}
	t, err := browser.ResolveTarget(ctx, a.client(), sid, a.currentRefs(), target)
	if err != nil {
		return nil, sid, err
	}
	return t, sid, nil
}

// finish resume o resultado de uma ação e anexa avisos de console.
func (a *Agent) finish(ctx context.Context, sess *browser.Session, sid, label string, errCountBefore int) string {
	sess.Settle(ctx, sid, actionIdle, actionTimeout)
	sess.UpdateHUD(ctx, label)
	var b strings.Builder
	fmt.Fprintf(&b, "ok: %s", label)
	newErrs := sess.Observe.Console(sid, "error", 0)
	if len(newErrs) > errCountBefore {
		for _, e := range newErrs[errCountBefore:] {
			fmt.Fprintf(&b, "\n!! console: %s", e.Text)
		}
	}
	url, _ := evalString(ctx, a.client(), sid, "location.href")
	fmt.Fprintf(&b, "\nurl: %s", url)
	return b.String()
}

func (a *Agent) errCount(sess *browser.Session, sid string) int {
	return len(sess.Observe.Console(sid, "error", 0))
}

func (a *Agent) clickLike(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	if target == "" {
		return protocol.Fail(fmt.Errorf("uso: bu %s <alvo>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if req.Cmd == "hover" {
		if err := browser.Hover(ctx, a.client(), sid, t); err != nil {
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
	if err := browser.Click(ctx, a.client(), sid, t, button, count); err != nil {
		return protocol.Fail(err)
	}
	action := "click"
	if count == 2 {
		action = "dblclick"
	}
	return ok(a.finish(ctx, sess, sid, action+" "+target, before))
}

func (a *Agent) fillLike(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	text := req.String("text")
	if target == "" {
		return protocol.Fail(fmt.Errorf("uso: bu %s <alvo> <texto>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if req.Cmd == "fill" {
		err = browser.Fill(ctx, a.client(), sid, t, text)
	} else {
		err = browser.Type(ctx, a.client(), sid, t, text)
	}
	if err != nil {
		return protocol.Fail(err)
	}
	label := fmt.Sprintf("%s %s = %s", req.Cmd, target, strconv.Quote(text))
	return ok(a.finish(ctx, sess, sid, label, before))
}

func (a *Agent) press(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	key := req.String("key")
	if key == "" {
		return protocol.Fail(fmt.Errorf("uso: bu press <tecla>"))
	}
	sid, err := a.activeSID(sess)
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
		return protocol.Fail(fmt.Errorf("uso: bu select <alvo> <valor>"))
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
		return protocol.Fail(fmt.Errorf("uso: bu %s <alvo>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	want := req.Cmd == "check"
	clicked, err := browser.SetChecked(ctx, a.client(), sid, t, want)
	if err != nil {
		return protocol.Fail(err)
	}
	label := req.Cmd + " " + target
	if !clicked {
		label += " (já estava)"
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}

func (a *Agent) scroll(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	raw := req.String("dy")
	if raw == "" {
		return protocol.Fail(fmt.Errorf("uso: bu scroll <dy> (dy negativo desce)"))
	}
	dy, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return protocol.Fail(fmt.Errorf("dy inválido: %q", raw))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	if err := browser.Scroll(ctx, a.client(), sid, 0, dy); err != nil {
		return protocol.Fail(err)
	}
	sess.Settle(ctx, sid, actionIdle, actionTimeout)
	sess.UpdateHUD(ctx, fmt.Sprintf("scroll %.0f", dy))
	return ok(fmt.Sprintf("ok: rolei %.0f", dy))
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
	text, err := evalString(ctx, a.client(), sid, expr)
	if err != nil {
		return protocol.Fail(err)
	}
	text = squeeze(text)
	if len(text) > 8000 {
		text = text[:8000] + "\n(... truncado)"
	}
	url, _ := evalString(ctx, a.client(), sid, "location.href")
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
	raw, err := a.client().Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    js,
		"returnByValue": true,
		"awaitPromise":  true,
	}, sid)
	if err != nil {
		return protocol.Fail(err)
	}
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return protocol.Fail(err)
	}
	if res.ExceptionDetails != nil {
		return protocol.Fail(fmt.Errorf("%s", res.ExceptionDetails.Text))
	}
	out := string(res.Result.Value)
	if out == "" {
		out = "undefined"
	}
	return ok(out)
}

func (a *Agent) tabs(sess *browser.Session) protocol.Response {
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
	url, _ := evalString(ctx, a.client(), sid, "location.href")
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

func (a *Agent) console(sess *browser.Session, req protocol.Request) protocol.Response {
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

func (a *Agent) net(sess *browser.Session, req protocol.Request) protocol.Response {
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
		switch {
		case e.Failed != "":
			fmt.Fprintf(&b, "FAIL %s %s (%s)\n", e.Method, e.URL, e.Failed)
		case e.Status >= 400:
			fmt.Fprintf(&b, "%d %s %s\n", e.Status, e.Method, e.URL)
		default:
			fmt.Fprintf(&b, "%d %s %s\n", e.Status, e.Method, e.URL)
		}
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}

func (a *Agent) shot(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	path := req.String("path")
	if path == "" {
		return protocol.Fail(fmt.Errorf("uso: bu shot <arquivo.png> [--full]"))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	data, err := browser.Screenshot(ctx, a.client(), sid, req.Bool("full", false))
	if err != nil {
		return protocol.Fail(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return protocol.Fail(err)
	}
	return ok(fmt.Sprintf("ok: %s (%d bytes)", path, len(data)))
}

// ---- roteiro ----

func (a *Agent) runScript(ctx context.Context, req protocol.Request) protocol.Response {
	content := req.String("content")
	if content == "" {
		path := req.String("path")
		if path == "" || path == "-" {
			return protocol.Fail(fmt.Errorf("roteiro vazio"))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return protocol.Fail(err)
		}
		content = string(data)
	}

	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		tokens, err := splitTokens(trimmed)
		if err != nil {
			fmt.Fprintf(&b, "> %s\n!! %v\n", trimmed, err)
			continue
		}
		sub, err := command.Parse(tokens)
		if err != nil {
			fmt.Fprintf(&b, "> %s\n!! %v\n", trimmed, err)
			continue
		}
		fmt.Fprintf(&b, "> %s\n", trimmed)
		if sub.Cmd == "script" {
			fmt.Fprintf(&b, "!! roteiro não pode chamar roteiro\n")
			continue
		}
		res := a.dispatch(ctx, sub)
		if res.OK {
			fmt.Fprintf(&b, "%s\n", res.Text)
		} else {
			fmt.Fprintf(&b, "!! %s\n", res.Error)
			break // para no primeiro erro: roteiro interrompido
		}
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}

// ---- utilitários ----

func ok(text string) protocol.Response {
	return protocol.Response{OK: true, Text: text}
}

func evalString(ctx context.Context, client *cdp.Client, session, expr string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("sem conexão")
	}
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	return res.Result.Value, nil
}

func pageHasText(ctx context.Context, client *cdp.Client, session, want string) (bool, error) {
	expr := fmt.Sprintf(`(() => {
		const body = document.body;
		if (!body) return false;
		return (body.innerText || '').includes(%s);
	})()`, strconv.Quote(want))
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
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
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, err
	}
	return res.Result.Value, nil
}

// squeeze reduz linhas em branco repetidas.
func squeeze(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// splitTokens divide uma linha respeitando aspas simples e duplas.
func splitTokens(line string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			has = true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("aspas não fechadas")
	}
	if has || cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}
