// Sessão do browser: descoberta e ciclo de vida das abas, navegação,
// convergência (esperar em vez de dormir) e diálogos.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/overlay"
)

// Tab é uma aba (target de página) com sua sessão CDP.
type Tab struct {
	TargetID  string
	SessionID string
	URL       string
	Title     string
	ready     chan struct{}
	initErr   error
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

	mu     sync.Mutex
	tabs   map[string]*Tab
	order  []string
	active string
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
func NewSession(ctx context.Context, client *cdp.Client, acceptDialogs bool) (*Session, error) {
	s := &Session{
		ctx:           ctx,
		client:        client,
		Observe:       NewObserve(500),
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
func (s *Session) remember(targetID, url, title string) *Tab {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tab, ok := s.tabs[targetID]; ok {
		if url != "" {
			tab.URL = url
		}
		if title != "" {
			tab.Title = title
		}
		return tab
	}
	tab := &Tab{
		TargetID: targetID,
		URL:      url,
		Title:    title,
		ready:    make(chan struct{}),
	}
	s.tabs[targetID] = tab
	s.order = append(s.order, targetID)
	if s.active == "" {
		s.active = targetID
	}
	return tab
}

// attachTarget anexa a uma aba registrada, uma única vez. Bloqueia num Send:
// nunca chame do laço de leitura sem goroutine.
func (s *Session) attachTarget(targetID, url, title string) {
	s.mu.Lock()
	tab := s.tabs[targetID]
	if tab == nil {
		tab = &Tab{TargetID: targetID, URL: url, Title: title, ready: make(chan struct{})}
		s.tabs[targetID] = tab
		s.order = append(s.order, targetID)
		if s.active == "" {
			s.active = targetID
		}
	}
	if tab.SessionID != "" || s.attaching[targetID] {
		s.mu.Unlock()
		return
	}
	s.attaching[targetID] = true
	s.mu.Unlock()

	var res struct {
		SessionID string `json:"sessionId"`
	}
	err := s.client.SendJSON(s.ctx, "Target.attachToTarget",
		map[string]any{"targetId": targetID, "flatten": true}, "", &res)

	s.mu.Lock()
	delete(s.attaching, targetID)
	if err == nil && res.SessionID != "" {
		tab.SessionID = res.SessionID
	}
	s.mu.Unlock()

	if err != nil || res.SessionID == "" {
		tab.initErr = fmt.Errorf("não consegui anexar à aba: %v", err)
		close(tab.ready)
		return
	}
	go func() {
		tab.initErr = s.initTab(tab)
		close(tab.ready)
	}()
}

func (s *Session) has(targetID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tabs[targetID]
	return ok
}

func (s *Session) initTab(tab *Tab) error {
	sid := tab.SessionID
	for _, method := range []string{
		"Page.enable", "Runtime.enable", "Network.enable",
		"DOM.enable", "Accessibility.enable", "Log.enable",
	} {
		if _, err := s.client.Send(s.ctx, method, map[string]any{}, sid); err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
	}
	if err := overlay.Install(s.ctx, s.client, sid); err != nil {
		return fmt.Errorf("overlay: %w", err)
	}

	s.client.On("Network.requestWillBeSent", func(params json.RawMessage, s2 string) {
		if s2 != sid {
			return
		}
		var p struct {
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(params, &p) == nil {
			s.startReq(sid, p.RequestID)
		}
	})
	done := func(params json.RawMessage, s2 string) {
		if s2 != sid {
			return
		}
		var p struct {
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(params, &p) == nil {
			s.doneReq(sid, p.RequestID)
		}
	}
	s.client.On("Network.loadingFinished", done)
	s.client.On("Network.loadingFailed", done)

	s.client.On("Page.javascriptDialogOpening", func(params json.RawMessage, s2 string) {
		if s2 != sid {
			return
		}
		// Precisa ser assíncrono: o handler roda no laço de leitura e
		// handleDialog faz um Send (que espera resposta do próprio laço).
		go s.handleDialog(sid, params)
	})
	return nil
}

func (s *Session) startReq(sid, requestID string) {
	if requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.inflight[sid]
	if set == nil {
		set = make(map[string]struct{})
		s.inflight[sid] = set
	}
	set[requestID] = struct{}{}
	s.lastActivity[sid] = time.Now()
}

func (s *Session) doneReq(sid, requestID string) {
	if requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight[sid], requestID)
	s.lastActivity[sid] = time.Now()
}

func (s *Session) remove(targetID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tabs, targetID)
	for i, id := range s.order {
		if id == targetID {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if s.active == targetID {
		s.active = ""
		if len(s.order) > 0 {
			s.active = s.order[0]
		}
	}
}

func (s *Session) removeBySession(sessionID string) {
	s.mu.Lock()
	targetID := ""
	for id, tab := range s.tabs {
		if tab.SessionID == sessionID {
			targetID = id
			break
		}
	}
	s.mu.Unlock()
	if targetID != "" {
		s.remove(targetID)
	}
}

// Tabs lista as abas na ordem em que apareceram.
func (s *Session) Tabs() []TabInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TabInfo, 0, len(s.order))
	for i, id := range s.order {
		tab, ok := s.tabs[id]
		if !ok {
			continue
		}
		out = append(out, TabInfo{
			Index:    i + 1,
			TargetID: id,
			Active:   id == s.active,
			Title:    tab.Title,
			URL:      tab.URL,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// activeTab devolve a aba ativa sem esperar anexação.
func (s *Session) activeTab() (*Tab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.active
	if id == "" && len(s.order) > 0 {
		id = s.order[0]
		s.active = id
	}
	tab := s.tabs[id]
	if tab == nil {
		return nil, fmt.Errorf("nenhuma aba aberta")
	}
	return tab, nil
}

// attachIfNeeded anexa a aba se ela ainda não estiver anexada.
func (s *Session) attachIfNeeded(tab *Tab) {
	s.mu.Lock()
	need := tab.SessionID == ""
	targetID, url, title := tab.TargetID, tab.URL, tab.Title
	s.mu.Unlock()
	if need {
		s.attachTarget(targetID, url, title)
	}
}

// Active devolve a aba ativa já pronta, anexando só ela se preciso.
func (s *Session) Active() (*Tab, error) {
	tab, err := s.activeTab()
	if err != nil {
		return nil, err
	}
	s.attachIfNeeded(tab)
	<-tab.ready
	if tab.initErr != nil {
		return nil, tab.initErr
	}
	return tab, nil
}

// ActiveSID devolve o sessionId da aba ativa.
func (s *Session) ActiveSID() (string, error) {
	tab, err := s.Active()
	if err != nil {
		return "", err
	}
	return tab.SessionID, nil
}

// Find localiza uma aba por targetId ou por índice 1-based.
func (s *Session) Find(ref string) (*Tab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		if id == ref {
			return s.tabs[id], nil
		}
	}
	var index int
	if _, err := fmt.Sscanf(ref, "%d", &index); err == nil && index >= 1 && index <= len(s.order) {
		return s.tabs[s.order[index-1]], nil
	}
	return nil, fmt.Errorf("aba %q não existe (use `bu tabs`)", ref)
}

// Select troca a aba ativa. `activate` traz a janela para a frente — por padrão
// NÃO fazemos isso: roubar foco a cada comando atrapalha quem está trabalhando
// em outra janela. A aba ativa do driver independe do foco do sistema.
func (s *Session) Select(ctx context.Context, ref string, activate bool) (*Tab, error) {
	tab, err := s.Find(ref)
	if err != nil {
		return nil, err
	}
	if activate {
		if _, err := s.client.Send(ctx, "Target.activateTarget",
			map[string]any{"targetId": tab.TargetID}, ""); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.active = tab.TargetID
	s.mu.Unlock()
	s.attachIfNeeded(tab)
	<-tab.ready
	if tab.initErr != nil {
		return nil, tab.initErr
	}
	return tab, nil
}

// NewTab abre uma aba e espera ela ficar pronta, sem trazer a janela para frente.
func (s *Session) NewTab(ctx context.Context, url string) (*Tab, error) {
	if url == "" {
		url = "about:blank"
	}
	var res struct {
		TargetID string `json:"targetId"`
	}
	// background=true: a aba nova não vira a aba em foco nem levanta a janela.
	if err := s.client.SendJSON(ctx, "Target.createTarget",
		map[string]any{"url": url, "background": true}, "", &res); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s.has(res.TargetID) {
			return s.Select(ctx, res.TargetID, false)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("aba nova não ficou pronta")
}

// CloseTab fecha uma aba.
func (s *Session) CloseTab(ctx context.Context, ref string) error {
	tab, err := s.Find(ref)
	if err != nil {
		return err
	}
	_, err = s.client.Send(ctx, "Target.closeTarget",
		map[string]any{"targetId": tab.TargetID}, "")
	return err
}

// Navigate vai para uma URL e espera convergir.
func (s *Session) Navigate(ctx context.Context, sid, url string, timeout time.Duration) error {
	var res struct {
		ErrorText string `json:"errorText"`
	}
	if err := s.client.SendJSON(ctx, "Page.navigate", map[string]any{"url": url}, sid, &res); err != nil {
		return err
	}
	if res.ErrorText != "" {
		return fmt.Errorf("navegação falhou: %s", res.ErrorText)
	}
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond, timeout)
	return nil
}

// HistoryMove anda no histórico (-1 volta, +1 avança).
func (s *Session) HistoryMove(ctx context.Context, sid string, delta int, timeout time.Duration) error {
	var hist struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := s.client.SendJSON(ctx, "Page.getNavigationHistory", map[string]any{}, sid, &hist); err != nil {
		return err
	}
	target := hist.CurrentIndex + delta
	if target < 0 || target >= len(hist.Entries) {
		return fmt.Errorf("sem histórico para %s", map[int]string{-1: "voltar", 1: "avançar"}[delta])
	}
	if _, err := s.client.Send(ctx, "Page.navigateToHistoryEntry",
		map[string]any{"entryId": hist.Entries[target].ID}, sid); err != nil {
		return err
	}
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond, timeout)
	return nil
}

// Reload recarrega a página.
func (s *Session) Reload(ctx context.Context, sid string, timeout time.Duration) error {
	if _, err := s.client.Send(ctx, "Page.reload", map[string]any{}, sid); err != nil {
		return err
	}
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond, timeout)
	return nil
}

// WaitForLoad espera o documento ficar completo.
func (s *Session) WaitForLoad(ctx context.Context, sid string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := s.client.Send(ctx, "Runtime.evaluate", map[string]any{
			"expression":    "document.readyState",
			"returnByValue": true,
		}, sid)
		if err == nil {
			var res struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			}
			if json.Unmarshal(raw, &res) == nil && res.Result.Value == "complete" {
				return nil
			}
		}
		time.Sleep(80 * time.Millisecond)
	}
	return fmt.Errorf("timeout esperando a página carregar")
}

// Settle espera a rede sossegar: sem requisições em voo por `idle`.
func (s *Session) Settle(ctx context.Context, sid string, idle, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		inflight := len(s.inflight[sid])
		last := s.lastActivity[sid]
		s.mu.Unlock()
		if inflight <= 0 && time.Since(last) >= idle {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

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
	_ = overlay.SetHUD(ctx, s.client, tab.SessionID,
		fmt.Sprintf("%d/%d", idx, len(infos)), label)
}
