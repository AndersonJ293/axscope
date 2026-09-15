// Navegação e convergência: ir para uma URL, histórico, recarregar e esperar a
// página assentar — em vez de dormir um tempo fixo.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

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
	s.Settle(ctx, sid, 300*time.Millisecond)
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
	s.Settle(ctx, sid, 300*time.Millisecond)
	return nil
}

// Reload recarrega a página.

func (s *Session) Reload(ctx context.Context, sid string, timeout time.Duration) error {
	if _, err := s.client.Send(ctx, "Page.reload", map[string]any{}, sid); err != nil {
		return err
	}
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond)
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

// settleCap é o teto da espera por sossego (ver Settle).
//
// Navegação e ação têm bolsas diferentes (45s / 8s), mas nenhuma delas é uma
// espera por sossego: é o tempo máximo para a página responder. Num app que
// nunca fica quieto — polling, websocket, telemetria — "sossegar" nunca chega,
// e usar a bolsa inteira aqui é só tempo perdido em toda ação.

const settleCap = 1500 * time.Millisecond

// Settle espera a rede sossegar: sem requisições em voo por `idle`, ou até
// settleCap. Quem precisa de mais usa `wait` (por texto), que é o critério
// confiável.

func (s *Session) Settle(ctx context.Context, sid string, idle time.Duration) {
	deadline := time.Now().Add(settleCap)
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
