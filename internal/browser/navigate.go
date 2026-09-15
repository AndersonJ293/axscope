// Navigation and convergence: go to a URL, history, reload and wait for the page
// to settle — instead of sleeping a fixed time.
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
	// background=true: the new tab does not become the focused tab nor raise the
	// window.
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
	return nil, fmt.Errorf("new tab did not become ready")
}

// CloseTab closes a tab.

func (s *Session) CloseTab(ctx context.Context, ref string) error {
	tab, err := s.Find(ref)
	if err != nil {
		return err
	}
	_, err = s.client.Send(ctx, "Target.closeTarget",
		map[string]any{"targetId": tab.TargetID}, "")
	return err
}

// Navigate goes to a URL and waits for convergence.

func (s *Session) Navigate(ctx context.Context, sid, url string, timeout time.Duration) error {
	var res struct {
		ErrorText string `json:"errorText"`
	}
	if err := s.client.SendJSON(ctx, "Page.navigate", map[string]any{"url": url}, sid, &res); err != nil {
		return err
	}
	if res.ErrorText != "" {
		return fmt.Errorf("navigation failed: %s", res.ErrorText)
	}
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond)
	return nil
}

// HistoryMove moves through the history (-1 back, +1 forward).

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
		return fmt.Errorf("no history to %s", map[int]string{-1: "back", 1: "forward"}[delta])
	}
	if _, err := s.client.Send(ctx, "Page.navigateToHistoryEntry",
		map[string]any{"entryId": hist.Entries[target].ID}, sid); err != nil {
		return err
	}
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond)
	return nil
}

// Reload reloads the page.

func (s *Session) Reload(ctx context.Context, sid string, timeout time.Duration) error {
	if _, err := s.client.Send(ctx, "Page.reload", map[string]any{}, sid); err != nil {
		return err
	}
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond)
	return nil
}

// WaitForLoad waits for the document to become complete.

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
	return fmt.Errorf("timeout waiting for the page to load")
}

// settleCap is the ceiling of the wait for quiet (see Settle).
//
// Navigation and action have different budgets (45s / 8s), but neither of them
// is a wait for quiet: it is the maximum time for the page to respond. In an app
// that never stays still — polling, websocket, telemetry — "settling" never
// comes, and using the whole budget here is just lost time on every action.

const settleCap = 1500 * time.Millisecond

// Settle waits for the network to settle: no requests in flight for `idle`, or
// up to settleCap. Whoever needs more uses `wait` (by text), which is the
// reliable criterion.

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
