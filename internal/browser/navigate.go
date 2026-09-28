// Navigation and convergence: go to a URL, history, reload and wait for the page
// to settle instead of sleeping a fixed time.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/AndersonJ293/axscope/internal/dom"
)

// longLived is how old an open request must be to stop counting as the page
// being busy. Some requests never finish from this tab's point of view: the
// document of a cross-origin iframe (reCAPTCHA's anchor) reports its start here
// and its end in the frame's own session, and an EventSource or long poll is
// open by design. Counted, they kept every action waiting out the settle cap
// (~1.5s a click on any page with reCAPTCHA) and network-idle waits failing.
const longLived = 5 * time.Second

type pendingReq struct {
	url   string
	start time.Time
}

func (s *Session) startReq(sid, requestID, url string) {
	if requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.inflight[sid]
	if set == nil {
		set = make(map[string]pendingReq)
		s.inflight[sid] = set
	}
	set[requestID] = pendingReq{url: url, start: time.Now()}
	s.lastActivity[sid] = time.Now()
}

// resetReqs forgets a tab's open requests when its main frame commits a new
// document: whatever the old page left open (a request whose end was reported
// elsewhere) is not the new page's work.
func (s *Session) resetReqs(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, sid)
}

// busyReqs returns the URLs of a tab's requests that still count as work in
// flight: the open ones younger than longLived. The caller holds s.mu.
func (s *Session) busyReqs(sid string, now time.Time) []string {
	var out []string
	for _, r := range s.inflight[sid] {
		if now.Sub(r.start) < longLived {
			out = append(out, r.url)
		}
	}
	return out
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
			return s.openIn(ctx, seed.TargetID, url)
		}
	}
	var res struct {
		TargetID string `json:"targetId"`
	}
	// background=true: the new tab does not become the focused tab nor raise the
	// window. It is born blank and then navigated, so the load wait has the blank
	// document to compare against instead of returning on it.
	if err := s.client.SendJSON(ctx, "Target.createTarget",
		map[string]any{"url": "about:blank", "background": true}, "", &res); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s.has(res.TargetID) {
			return s.openIn(ctx, res.TargetID, url)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("new tab did not become ready")
}

// openIn makes a target the active one and, when a URL is wanted, navigates it
// and waits for convergence — the same wait the seed path gets, so `newtab` does
// not return while the page it opened is still blank.
func (s *Session) openIn(ctx context.Context, targetID, url string) (*Tab, error) {
	tab, err := s.Select(ctx, targetID, false)
	if err != nil {
		return nil, err
	}
	if url != "" && url != "about:blank" {
		if err := s.Navigate(ctx, tab.SessionID, url, 15*time.Second, false); err != nil {
			return nil, err
		}
	}
	return tab, nil
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
	from := s.mark(ctx, sid)
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
	_ = s.WaitForLoad(ctx, sid, from, timeout)
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
	from := s.mark(ctx, sid)
	if _, err := s.client.Send(ctx, "Page.navigateToHistoryEntry",
		map[string]any{"entryId": hist.Entries[target].ID}, sid); err != nil {
		return err
	}
	// The load wait is advisory; Settle caps the total wait below.
	_ = s.WaitForLoad(ctx, sid, from, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond)
	return nil
}

// docMark is where a navigation starts from: the current document's token and
// URL, and when the command was issued. A wait compares them, because the
// previous document keeps reporting `complete` for a moment after a navigation
// begins — a plain readyState check returns on the old page.
type docMark struct {
	doc  string
	url  string
	when time.Time
}

// mark records where a navigation starts from. An empty doc means the reading
// failed; the wait then falls back to a plain readyState check.
func (s *Session) mark(ctx context.Context, sid string) docMark {
	from := docMark{when: time.Now()}
	// `performance.timeOrigin` is fresh for each document, so it names the page;
	// the URL catches a same-document navigation (a hash), which keeps the page.
	raw, err := dom.EvalString(ctx, s.client, sid, "JSON.stringify([String(performance.timeOrigin), location.href])")
	if err != nil {
		return from
	}
	if parts, ok := parseFields(raw, 2); ok {
		from.doc, from.url = parts[0], parts[1]
	}
	return from
}

// parseFields parses the JSON array a document reading answers, requiring n
// fields so a malformed or half-written answer is not taken for a document.
func parseFields(value string, n int) ([]string, bool) {
	var parts []string
	if json.Unmarshal([]byte(value), &parts) != nil || len(parts) != n {
		return nil, false
	}
	return parts, true
}

// Reload reloads the page. hard bypasses the cache, the standard remedy for a
// dead UI.
func (s *Session) Reload(ctx context.Context, sid string, timeout time.Duration, hard bool) error {
	from := s.mark(ctx, sid)
	if _, err := s.client.Send(ctx, "Page.reload", map[string]any{"ignoreCache": hard}, sid); err != nil {
		return err
	}
	// The load wait is advisory; Settle caps the total wait below.
	_ = s.WaitForLoad(ctx, sid, from, timeout)
	s.Settle(ctx, sid, 300*time.Millisecond)
	return nil
}

// WaitForURL polls location.href until match agrees with present (`wait` for a
// match, `waitgone` for its absence) and returns the last URL seen, so a timeout
// can name it. A SPA changes the URL without any text appearing, so the text
// wait misses it.
func (s *Session) WaitForURL(ctx context.Context, sid string, present bool, match func(string) bool, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		if ctx.Err() != nil {
			return last, false
		}
		if href, err := dom.EvalString(ctx, s.client, sid, "location.href"); err == nil {
			last = href
			if match(href) == present {
				return last, true
			}
		}
		if !time.Now().Before(deadline) {
			return last, false
		}
		time.Sleep(120 * time.Millisecond)
	}
}

// WaitForNetworkIdle waits until no request has been in flight for `idle`, up to
// `timeout`. It reports whether the network went idle, and on timeout returns the
// URLs still pending so the refusal can name them. A page that renders in
// cascades is quiet only after the last fetch, which Settle's short cap misses.
func (s *Session) WaitForNetworkIdle(ctx context.Context, sid string, idle, timeout time.Duration) ([]string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		s.mu.Lock()
		pending := s.busyReqs(sid, time.Now())
		last := s.lastActivity[sid]
		s.mu.Unlock()
		if len(pending) == 0 && time.Since(last) >= idle {
			return nil, true
		}
		if !time.Now().Before(deadline) {
			sort.Strings(pending)
			return pending, false
		}
		select {
		case <-ctx.Done():
			return pending, false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// WaitForLoad waits for a navigation to land: the document must be complete and
// be the new one (a different `performance.timeOrigin`, or the same document
// with a new URL for a same-document navigation). Without that, the previous
// document — still reporting `complete` just after the navigation starts — would
// satisfy the wait, and an eval right after `reload` could read the old page. A
// beforeunload dialog blocks the navigation so it never lands; the wait gives up
// then, and the caller can name the dialog.
func (s *Session) WaitForLoad(ctx context.Context, sid string, from docMark, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		doc, state, url := s.docState(ctx, sid)
		if state == "complete" && (from.doc == "" || doc != from.doc || url != from.url) {
			return nil
		}
		if beforeUnloadSince(s.Observe.Dialogs(sid), from.when) {
			return nil
		}
		time.Sleep(80 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for the page to load")
}

// docState reads the document's token, readyState and URL in one round trip. It
// answers empty strings while the execution context is being replaced, which the
// caller reads as "not yet".
func (s *Session) docState(ctx context.Context, sid string) (doc, state, url string) {
	raw, err := s.client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "JSON.stringify([String(performance.timeOrigin), document.readyState, location.href])",
		"returnByValue": true,
	}, sid)
	if err != nil {
		return "", "", ""
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return "", "", ""
	}
	if parts, ok := parseFields(res.Result.Value, 3); ok {
		return parts[0], parts[1], parts[2]
	}
	return "", "", ""
}

// settleCap is the ceiling of the wait for quiet. Navigation and action have
// different budgets (45s / 8s), but both are the maximum time for the page to
// respond, not a wait for quiet — an app that never stays still never settles.
const settleCap = 1500 * time.Millisecond

// actionGrace is how long after an action a request it triggers may still
// start (a handler that fetches on the next tick, a debounce's first beat).
const actionGrace = 100 * time.Millisecond

// Settle lets the page answer the action, up to settleCap; `wait` by text is
// the reliable criterion for more. A quiet page (nothing in flight, no activity
// for `idle`) is settled at once. Otherwise the requests in flight once the
// action had its grace are waited for until they end — only those: a page that
// polls or streams telemetry starts new ones forever, and waiting for a window
// with none made every action on such a site pay the whole cap.
func (s *Session) Settle(ctx context.Context, sid string, idle time.Duration) {
	deadline := time.Now().Add(settleCap)
	if s.strictQuiet(sid, idle) {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(actionGrace):
	}
	if s.strictQuiet(sid, idle) {
		return
	}
	s.Drain(ctx, sid, deadline)
}

// strictQuiet is the old rule: nothing in flight and no activity for idle.
func (s *Session) strictQuiet(sid string, idle time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.busyReqs(sid, time.Now())) == 0 && time.Since(s.lastActivity[sid]) >= idle
}

// Drain waits until the requests in flight now have all ended (new ones do not
// extend the wait), or the deadline passes; it reports whether they ended.
func (s *Session) Drain(ctx context.Context, sid string, deadline time.Time) bool {
	s.mu.Lock()
	waiting := s.busyIDs(sid, time.Now())
	s.mu.Unlock()
	for len(waiting) > 0 {
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(30 * time.Millisecond):
		}
		s.mu.Lock()
		now := time.Now()
		for id := range waiting {
			r, open := s.inflight[sid][id]
			if !open || now.Sub(r.start) >= longLived {
				delete(waiting, id)
			}
		}
		s.mu.Unlock()
	}
	return true
}

// busyIDs is busyReqs by request id. The caller holds s.mu.
func (s *Session) busyIDs(sid string, now time.Time) map[string]bool {
	out := map[string]bool{}
	for id, r := range s.inflight[sid] {
		if now.Sub(r.start) < longLived {
			out[id] = true
		}
	}
	return out
}

// Pending lists the requests of a tab that still count as work in flight.
func (s *Session) Pending(sid string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.busyReqs(sid, time.Now())
	sort.Strings(out)
	return out
}
