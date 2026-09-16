// Browser session: the model (tabs, active target, observers) and the connection
// lifecycle.
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

// Tab is a tab (a page target) with its CDP session.
type Tab struct {
	TargetID  string
	SessionID string
	URL       string
	Title     string
	ready     chan struct{}
	initErr   error
	// tried marks that an attach was already attempted (successfully or not), so
	// it is not retried and the channel is not closed twice.
	tried bool
	once  sync.Once
}

// finish closes the ready channel exactly once.
func (t *Tab) finish() {
	t.once.Do(func() { close(t.ready) })
}

// TabInfo is what the agent sees in `tabs`.
type TabInfo struct {
	Index    int    `json:"index"`
	TargetID string `json:"targetId"`
	Active   bool   `json:"active"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// Session keeps the tabs alive and the convergence state.
type Session struct {
	ctx     context.Context
	client  *cdp.Client
	Observe *Observe
	// Presenter draws the action in the browser. The domain does not know the
	// implementation — it is injected by whoever builds the session.
	Presenter Presenter

	mu     sync.Mutex
	tabs   map[string]*Tab
	order  []string
	active string
	// seedID is the about:blank page created at boot (some engines are not born
	// with one). The first NewTab reuses it, so no empty tab is stranded.
	seedID string
	// activeFile is where the active tab is remembered, and prefActive what was
	// stored. A daemon restart cannot swap the active tab under the agent.
	activeFile string
	prefActive string
	// attaching avoids attaching twice to the same target (a race between
	// targetCreated and the bootstrap's explicit attach).
	attaching map[string]bool

	inflight     map[string]map[string]string
	lastActivity map[string]time.Time

	acceptDialogs bool
	// forceUnload accepts the next beforeunload dialog (an explicit `open
	// --force`), so leaving a page with unsaved changes is possible on request.
	forceUnload bool
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

// NewSession wires target discovery and the existing tabs.
func NewSession(ctx context.Context, client *cdp.Client, acceptDialogs bool, presenter Presenter) (*Session, error) {
	s := &Session{
		ctx:           ctx,
		client:        client,
		Observe:       NewObserve(500),
		Presenter:     presenter,
		tabs:          make(map[string]*Tab),
		attaching:     make(map[string]bool),
		inflight:      make(map[string]map[string]string),
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
	// Attach from targetCreated (discover), not setAutoAttach: autoAttach plus an
	// explicit attach created two sessions for the same target.
	s.client.On("Target.targetCreated", func(params json.RawMessage, _ string) {
		var p struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(params, &p) != nil || p.TargetInfo.Type != "page" {
			return
		}
		// The handler runs on the read loop: it cannot block on a Send, so it
		// only registers — the attachment happens on demand.
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
	// All pages are registered, but only the one really used is attached; in a
	// user browser that can be dozens of tabs, and attaching to all is invasive.
	for _, ti := range got.TargetInfos {
		if ti.Type != "page" {
			continue
		}
		s.remember(ti.TargetID, ti.URL, ti.Title)
	}

	// Engines like chrome-headless-shell are not born with a page: it must be
	// created. Without that the daemon stays alive with no tab at all.
	if len(s.Tabs()) == 0 {
		var created struct {
			TargetID string `json:"targetId"`
		}
		if err := s.client.SendJSON(ctx, "Target.createTarget",
			map[string]any{"url": "about:blank", "background": true}, "", &created); err == nil && created.TargetID != "" {
			s.remember(created.TargetID, "about:blank", "")
			s.mu.Lock()
			s.seedID = created.TargetID
			s.mu.Unlock()
		}
	}

	// Attach the active tab (the first in the list: the one the user is seeing).
	if _, err := s.Active(); err != nil {
		return nil // no tab yet; the next command reports it
	}
	return nil
}

// SetActiveFile points to where the active tab is remembered and loads it.
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
	// The bootstrap already chose a tab before this call; the preference comes
	// afterward and rules — as long as the tab still exists.
	if _, ok := s.tabs[id]; ok {
		s.active = id
	}
}

// saveActive stores the active tab (outside the lock: a disk error does not
// block the rest).
func (s *Session) saveActive() {
	s.mu.Lock()
	path, id := s.activeFile, s.active
	s.mu.Unlock()
	if path == "" || id == "" {
		return
	}
	_ = os.WriteFile(path, []byte(id), 0o644)
}

// ForceUnload makes the beforeunload dialog of the navigation about to run
// accept (leave the page) instead of dismiss (stay). `open --force` sets it for
// the duration of the navigation.
func (s *Session) ForceUnload(on bool) {
	s.mu.Lock()
	s.forceUnload = on
	s.mu.Unlock()
}

// handleDialog records and handles a native dialog.
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
	if p.Type == "beforeunload" {
		// Accepting a beforeunload means leaving the page; dismissing means
		// staying, which aborts the navigation. Only a forced navigation leaves.
		s.mu.Lock()
		accept = s.forceUnload || s.acceptDialogs
		s.mu.Unlock()
	}
	if accept {
		handled = "accept"
	}
	s.Observe.addDialog(sid, DialogEntry{
		Time:    time.Now(),
		Type:    p.Type,
		Message: p.Message,
		Handled: handled,
	})
	// Best effort: a failed dialog command has no recovery.
	_, _ = s.client.Send(s.ctx, "Page.handleJavaScriptDialog",
		map[string]any{"accept": accept}, sid)
}

// UpdateHUD shows tabs + the last action's label in the overlay.
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
