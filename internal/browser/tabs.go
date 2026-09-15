// Tab registry: discovery, attachment on demand and switching the active tab.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

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
	if s.canActivate(targetID) {
		s.active = targetID
	}
	return tab
}

// canActivate decides whether the tab can become active: only the one that was
// stored, or any one when there is no preference.

func (s *Session) canActivate(targetID string) bool {
	return s.active == "" && (s.prefActive == "" || targetID == s.prefActive)
}

// SetActiveFile points to where the active tab is remembered and loads what is
// there.

func (s *Session) attachTarget(targetID, url, title string) {
	s.mu.Lock()
	tab := s.tabs[targetID]
	if tab == nil {
		tab = &Tab{TargetID: targetID, URL: url, Title: title, ready: make(chan struct{})}
		s.tabs[targetID] = tab
		s.order = append(s.order, targetID)
		if s.canActivate(targetID) {
			s.active = targetID
		}
	}
	if tab.SessionID != "" || s.attaching[targetID] || tab.tried {
		s.mu.Unlock()
		return
	}
	tab.tried = true
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
		tab.initErr = fmt.Errorf("could not attach to the tab: %v", err)
		tab.finish()
		return
	}
	go func() {
		tab.initErr = s.initTab(tab)
		tab.finish()
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
	if err := s.Presenter.Install(s.ctx, s.client, sid); err != nil {
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
		// It must be asynchronous: the handler runs on the read loop and
		// handleDialog does a Send (which waits for a response from the loop
		// itself).
		go s.handleDialog(sid, params)
	})
	return nil
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

// Tabs lists the tabs in the order they appeared.

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

// activeTab returns the active tab without waiting for attachment.

func (s *Session) activeTab() (*Tab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.active
	if id == "" {
		if _, ok := s.tabs[s.prefActive]; ok {
			id = s.prefActive
		}
	}
	if id == "" && len(s.order) > 0 {
		id = s.order[0]
	}
	s.active = id
	tab := s.tabs[id]
	if tab == nil {
		return nil, fmt.Errorf("no tab open")
	}
	return tab, nil
}

// attachIfNeeded attaches the tab if it is not attached yet.

func (s *Session) attachIfNeeded(tab *Tab) {
	s.mu.Lock()
	need := tab.SessionID == ""
	targetID, url, title := tab.TargetID, tab.URL, tab.Title
	s.mu.Unlock()
	if need {
		s.attachTarget(targetID, url, title)
	}
}

// Active returns the active tab already ready, attaching only it if needed.

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

// ActiveSID returns the sessionId of the active tab.

func (s *Session) ActiveSID() (string, error) {
	tab, err := s.Active()
	if err != nil {
		return "", err
	}
	return tab.SessionID, nil
}

// Find locates a tab by targetId or by 1-based index.

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
	return nil, fmt.Errorf("tab %q does not exist (use `axscope tabs`)", ref)
}

// Select switches the active tab. `activate` brings the window forward — by
// default we do NOT do that: stealing focus on every command gets in the way of
// whoever is working in another window. The driver's active tab does not depend
// on the system focus.

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
	s.saveActive()
	s.attachIfNeeded(tab)
	<-tab.ready
	if tab.initErr != nil {
		return nil, tab.initErr
	}
	return tab, nil
}

// NewTab opens a tab and waits for it to be ready, without bringing the window
// forward.
