// Sessão do browser: o modelo (abas, alvo ativo, observadores) e o ciclo de
// vida da conexão.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Tab é uma aba (target de página) com sua sessão CDP.
type Tab struct {
	TargetID  string
	SessionID string
	URL       string
	Title     string
	ready     chan struct{}
	initErr   error
	// tried marca que já tentamos anexar (com sucesso ou não), para não tentar
	// de novo e fechar o canal duas vezes.
	tried bool
	once  sync.Once
}

// finish fecha o canal de pronto exatamente uma vez.

func (t *Tab) finish() {
	t.once.Do(func() { close(t.ready) })
}

// TabInfo é o que o agente vê em `tabs`.

type TabInfo struct {
	Index    int    `json:"index"`
	TargetID string `json:"targetId"`
	Active   bool   `json:"active"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// Session mantém as abas vivas e o estado de convergência.

type Session struct {
	ctx     context.Context
	client  *cdp.Client
	Observe *Observe
	// Presenter desenha a ação no navegador. O domínio não conhece a
	// implementação — ela é injetada por quem constrói a sessão.
	Presenter Presenter

	mu     sync.Mutex
	tabs   map[string]*Tab
	order  []string
	active string
	// activeFile é onde a aba ativa é lembrada, e prefActive é o que estava
	// gravado. Reiniciar o daemon não pode trocar a aba ativa por baixo do
	// agente — o primeiro comando depois iria para a aba errada.
	activeFile string
	prefActive string
	// attaching evita anexar duas vezes ao mesmo target (corrida entre o
	// targetCreated e o attach explícito do bootstrap).
	attaching map[string]bool

	inflight     map[string]map[string]struct{}
	lastActivity map[string]time.Time

	acceptDialogs bool
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

// NewSession liga a descoberta de targets e as abas existentes.

func NewSession(ctx context.Context, client *cdp.Client, acceptDialogs bool, presenter Presenter) (*Session, error) {
	s := &Session{
		ctx:           ctx,
		client:        client,
		Observe:       NewObserve(500),
		Presenter:     presenter,
		tabs:          make(map[string]*Tab),
		attaching:     make(map[string]bool),
		inflight:      make(map[string]map[string]struct{}),
		lastActivity:  make(map[string]time.Time),
		acceptDialogs: acceptDialogs,
	}
	s.Observe.Wire(client)
	s.wireTargets()
	if err := s.bootstrap(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) wireTargets() {
	// Anexamos a partir de targetCreated (discover), e não de setAutoAttach:
	// autoAttach + attach explícito criavam duas sessões para o mesmo target e
	// o comando ia para a sessão errada (Page.enable travava).
	s.client.On("Target.targetCreated", func(params json.RawMessage, _ string) {
		var p struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(params, &p) != nil || p.TargetInfo.Type != "page" {
			return
		}
		// Handler roda no laço de leitura: não pode bloquear num Send, e aqui
		// só registramos — a anexação acontece sob demanda.
		s.remember(p.TargetInfo.TargetID, p.TargetInfo.URL, p.TargetInfo.Title)
	})

	s.client.On("Target.detachedFromTarget", func(params json.RawMessage, _ string) {
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		s.removeBySession(p.SessionID)
	})

	s.client.On("Target.targetDestroyed", func(params json.RawMessage, _ string) {
		var p struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		s.remove(p.TargetID)
	})

	s.client.On("Target.targetInfoChanged", func(params json.RawMessage, _ string) {
		var p struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if tab, ok := s.tabs[p.TargetInfo.TargetID]; ok {
			tab.URL = p.TargetInfo.URL
			tab.Title = p.TargetInfo.Title
		}
	})
}

func (s *Session) bootstrap(ctx context.Context) error {
	if _, err := s.client.Send(ctx, "Target.setDiscoverTargets",
		map[string]any{"discover": true}, ""); err != nil {
		return err
	}

	var got struct {
		TargetInfos []targetInfo `json:"targetInfos"`
	}
	if err := s.client.SendJSON(ctx, "Target.getTargets", map[string]any{}, "", &got); err != nil {
		return err
	}
	// Conhecemos todas as abas, mas só anexamos a que for realmente usada.
	// No navegador do usuário isso pode ser dezenas de abas; anexar em todas
	// seria invasivo (faixa de depuração em cada uma, overhead, conflito com
	// o DevTools aberto).
	for _, ti := range got.TargetInfos {
		if ti.Type != "page" {
			continue
		}
		s.remember(ti.TargetID, ti.URL, ti.Title)
	}

	// Engines como chrome-headless-shell não nascem com uma página: é preciso
	// criá-la. Sem isso o daemon fica vivo e sem aba nenhuma.
	if len(s.Tabs()) == 0 {
		var created struct {
			TargetID string `json:"targetId"`
		}
		if err := s.client.SendJSON(ctx, "Target.createTarget",
			map[string]any{"url": "about:blank", "background": true}, "", &created); err == nil && created.TargetID != "" {
			s.remember(created.TargetID, "about:blank", "")
		}
	}

	// Anexa a aba ativa (a primeira da lista: a que o usuário está vendo).
	if _, err := s.Active(); err != nil {
		return nil // sem aba ainda; o comando seguinte reporta
	}
	return nil
}

// remember registra uma aba sem anexar a ela.

func (s *Session) SetActiveFile(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeFile = path
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return
	}
	s.prefActive = id
	// O bootstrap já escolheu uma aba antes desta chamada; a preferência vem
	// depois e manda — desde que a aba ainda exista.
	if _, ok := s.tabs[id]; ok {
		s.active = id
	}
}

// saveActive grava a aba ativa (fora do lock: erro de disco não trava o resto).

func (s *Session) saveActive() {
	s.mu.Lock()
	path, id := s.activeFile, s.active
	s.mu.Unlock()
	if path == "" || id == "" {
		return
	}
	_ = os.WriteFile(path, []byte(id), 0o644)
}

// attachTarget anexa a uma aba registrada, uma única vez. Bloqueia num Send:
// nunca chame do laço de leitura sem goroutine.

func (s *Session) handleDialog(sid string, params json.RawMessage) {
	var p struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	handled := "dismiss"
	accept := s.acceptDialogs
	if accept {
		handled = "accept"
	}
	s.Observe.addDialog(sid, DialogEntry{
		Time:    time.Now(),
		Type:    p.Type,
		Message: p.Message,
		Handled: handled,
	})
	_, _ = s.client.Send(s.ctx, "Page.handleJavaScriptDialog",
		map[string]any{"accept": accept}, sid)
}

// UpdateHUD mostra abas + rótulo da última ação no overlay.

func (s *Session) UpdateHUD(ctx context.Context, label string) {
	tab, err := s.Active()
	if err != nil {
		return
	}
	infos := s.Tabs()
	idx := 0
	for _, t := range infos {
		if t.Active {
			idx = t.Index
		}
	}
	_ = s.Presenter.SetHUD(ctx, s.client, tab.SessionID,
		fmt.Sprintf("%d/%d", idx, len(infos)), label)
}
