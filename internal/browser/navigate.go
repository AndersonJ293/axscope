// Navigation and convergence: go to a URL, history, reload and wait for the page
// to settle instead of sleeping a fixed time.
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

// NewTab opens a tab and waits for it to be ready, without bringing the window
// forward.
func (s *Session) NewTab(ctx context.Context, url string) (*Tab, error) {
	if url == "" {
		url = "about:blank"
	}
	// The session seeds an about:blank at boot (some engines are born without a
	// page). Opening a URL reuses that seed instead of stranding an empty tab.
	if url != "about:blank" {
		if seed := s.takeSeed(); seed != nil {
			tab, err := s.Select(ctx, seed.TargetID, false)
			if err != nil {
				return nil, err
			}
			if err := s.Navigate(ctx, tab.SessionID, url, 15*time.Second, false); err != nil {
				return nil, err
			}
			return tab, nil
		}
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

// takeSeed returns the boot about:blank page while it is still blank, clearing
// the mark so only the first NewTab claims it.
func (s *Session) takeSeed() *Tab {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seedID == "" {
		return nil
	}
	tab := s.tabs[s.seedID]
	s.seedID = ""
	if tab == nil || (tab.URL != "" && tab.URL != "about:blank") {
		return nil
	}
	return tab
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

// Navigate goes to a URL and waits for convergence. force accepts a
// beforeunload dialog opened by the page (unsaved changes), so an explicit
// navigation can leave it.
func (s *Session) Navigate(ctx context.Context, sid, url string, timeout time.Duration, force bool) error {
	start := time.Now()
	if force {
		s.ForceUnload(true)
	}
	var res struct {
		ErrorText string `json:"errorText"`
	}
	err := s.client.SendJSON(ctx, "Page.navigate", map[string]any{"url": url}, sid, &res)
	if force {
		s.ForceUnload(false)
	}
	if err != nil {
		return err
	}
	if res.ErrorText != "" {
		return navigationError(s.Observe.Dialogs(sid), res.ErrorText, start)
	}
	// The load wait is advisory; Settle caps the total wait below.
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond)
	return nil
}

// navigationError names the cause when a beforeunload dialog aborted the
// navigation: a raw net::ERR_ABORTED hides both the reason and the way out.
func navigationError(dialogs []DialogEntry, errorText string, since time.Time) error {
	if errorText == "net::ERR_ABORTED" && beforeUnloadSince(dialogs, since) {
		return fmt.Errorf("the page has unsaved changes and blocked the navigation — leave it with `open --force`")
	}
	return fmt.Errorf("navigation failed: %s", errorText)
}

// beforeUnloadSince reports whether a beforeunload dialog was registered at or
// after `since` (the moment the navigation started).
func beforeUnloadSince(dialogs []DialogEntry, since time.Time) bool {
	for i := len(dialogs) - 1; i >= 0; i-- {
		if dialogs[i].Time.Before(since) {
			break
		}
		if dialogs[i].Type == "beforeunload" {
			return true
		}
	}
	return false
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
	// The load wait is advisory; Settle caps the total wait below.
	_ = s.WaitForLoad(ctx, sid, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond)
	return nil
}

// Reload reloads the page.
func (s *Session) Reload(ctx context.Context, sid string, timeout time.Duration) error {
	if _, err := s.client.Send(ctx, "Page.reload", map[string]any{}, sid); err != nil {
		return err
	}
	// The load wait is advisory; Settle caps the total wait below.
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

// settleCap is the ceiling of the wait for quiet. Navigation and action have
// different budgets (45s / 8s), but both are the maximum time for the page to
// respond, not a wait for quiet — an app that never stays still never settles.
const settleCap = 1500 * time.Millisecond

// Settle waits for the network to settle: no requests in flight for `idle`, or up
// to settleCap; `wait` by text is the reliable criterion for more.
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
