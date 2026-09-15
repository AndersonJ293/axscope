// Agente: executa os comandos sobre a sessão do browser e devolve texto para o
// agente de IA ler. É o mesmo despachante para CLI, daemon (roteiro) e MCP.
package agent

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ajunior/browser-use/internal/bridge"
	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/paths"
	"github.com/ajunior/browser-use/internal/protocol"
	"github.com/ajunior/browser-use/internal/render"
)

const (
	actionIdle = 300 * time.Millisecond
	navTimeout = 45 * time.Second
)

// Agent mantém o browser e o estado entre comandos.
type Agent struct {
	Session  string
	Attach   string
	Headless bool
	Engine   string

	runMu sync.Mutex

	mu     sync.Mutex
	handle *browser.Handle
	sess   *browser.Session
	refs   map[string]int
	// snapGen é a geração da última leitura. Toda ref carrega a geração em que
	// nasceu (e12#7): ref de leitura antiga é recusada, em vez de clicar no que
	// hoje ocupa aquela posição.
	snapGen   int
	bridge    *bridge.Server
	extClient *cdp.Client
	agent     string
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
		srv.SetLabel(a.agent)
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
	sess, err := browser.NewSession(ctx, handle.Client, false, render.Presenter{})
	if err != nil {
		handle.Client.Close()
		return nil, err
	}
	sess.SetActiveFile(paths.ActiveTabPath(a.Session))
	a.handle = handle
	a.sess = sess
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

// setAgent registra quem está dirigindo e reavisa a extensão, que renomeia o
// grupo de abas (mantendo o número que ele já tinha).
func (a *Agent) setAgent(name string) {
	a.mu.Lock()
	if name == "" || a.agent == name {
		a.mu.Unlock()
		return
	}
	a.agent = name
	srv := a.bridge
	a.mu.Unlock()
	if srv != nil {
		srv.SetLabel(name)
	}
}

// Run executa um pedido. Serializa tudo para não misturar ações.
func (a *Agent) Run(ctx context.Context, req protocol.Request) protocol.Response {
	a.runMu.Lock()
	defer a.runMu.Unlock()

	a.setAgent(req.Agent)
	return a.dispatch(ctx, req)
}

// activeSID devolve o sessionId da aba ativa da sessão.
func (a *Agent) activeSID(sess *browser.Session) (string, error) {
	return sess.ActiveSID()
}

func (a *Agent) mustSess() *browser.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sess
}

// ok monta uma resposta de sucesso.
func ok(text string) protocol.Response {
	return protocol.Response{OK: true, Text: text}
}
