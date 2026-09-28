package browser

import (
	"context"
	"testing"
	"time"
)

// The boot about:blank is claimed by the first NewTab and only while it is still
// blank; otherwise opening a URL would strand the empty tab (or reuse a page the
// user already navigated).
func TestTakeSeed(t *testing.T) {
	s := &Session{tabs: map[string]*Tab{}}
	if got := s.takeSeed(); got != nil {
		t.Fatalf("takeSeed with no seed = %v, want nil", got)
	}

	seed := &Tab{TargetID: "seed", URL: "about:blank"}
	s.tabs["seed"] = seed
	s.seedID = "seed"
	if got := s.takeSeed(); got != seed {
		t.Fatalf("takeSeed = %v, want the seed tab", got)
	}
	if got := s.takeSeed(); got != nil {
		t.Fatalf("second takeSeed = %v, want nil (the seed is claimed once)", got)
	}

	moved := &Tab{TargetID: "moved", URL: "https://example.com"}
	s.tabs["moved"] = moved
	s.seedID = "moved"
	if got := s.takeSeed(); got != nil {
		t.Fatalf("takeSeed on a navigated tab = %v, want nil", got)
	}
}

// TabIndex is the `tab <n>` ref a response prints so the caller does not need a
// `tabs` round trip after `open`/`newtab`.
func TestTabIndex(t *testing.T) {
	s := &Session{
		order: []string{"a", "b", "c"},
		tabs:  map[string]*Tab{"a": {}, "b": {}, "c": {}},
	}
	if idx, ok := s.TabIndex("b"); !ok || idx != 2 {
		t.Errorf("TabIndex(b) = %d, %v, want 2, true", idx, ok)
	}
	if _, ok := s.TabIndex("z"); ok {
		t.Error("TabIndex(z) reported a tab that is not there")
	}
}

// The network-idle wait returns as soon as the quiet window is met, and on
// timeout names the URLs still in flight.
func TestWaitForNetworkIdle(t *testing.T) {
	quiet := &Session{
		inflight:     map[string]map[string]pendingReq{"sid": {}},
		lastActivity: map[string]time.Time{"sid": time.Now().Add(-time.Second)},
	}
	if pending, idle := quiet.WaitForNetworkIdle(context.Background(), "sid", 50*time.Millisecond, time.Second); !idle || len(pending) != 0 {
		t.Errorf("idle network: pending=%v idle=%v, want empty/true", pending, idle)
	}

	busy := &Session{
		inflight: map[string]map[string]pendingReq{"sid": {
			"r1": {url: "https://a/one", start: time.Now()},
			"r2": {url: "https://a/two", start: time.Now()},
		}},
		lastActivity: map[string]time.Time{"sid": time.Now()},
	}
	pending, idle := busy.WaitForNetworkIdle(context.Background(), "sid", 50*time.Millisecond, 120*time.Millisecond)
	if idle {
		t.Fatal("a network with requests in flight must not report idle")
	}
	if len(pending) != 2 || pending[0] != "https://a/one" || pending[1] != "https://a/two" {
		t.Errorf("pending = %v, want the two sorted URLs", pending)
	}
}

// A request open past longLived (a cross-origin iframe's document, an
// EventSource) stops counting as work, and a new main document clears the old
// page's leftovers — otherwise every action on a reCAPTCHA page waited out the
// settle cap.
func TestBusyRequestsIgnoreLongLived(t *testing.T) {
	s := &Session{inflight: map[string]map[string]pendingReq{}, lastActivity: map[string]time.Time{}}
	s.startReq("t1", "fresh", "https://example.com/api")
	s.startReq("t1", "anchor", "https://www.google.com/recaptcha/enterprise/anchor")
	s.inflight["t1"]["anchor"] = pendingReq{url: "https://www.google.com/recaptcha/enterprise/anchor", start: time.Now().Add(-2 * longLived)}

	busy := s.busyReqs("t1", time.Now())
	if len(busy) != 1 || busy[0] != "https://example.com/api" {
		t.Errorf("busy = %v, want only the fresh request", busy)
	}
	s.doneReq("t1", "fresh")
	if busy := s.busyReqs("t1", time.Now()); len(busy) != 0 {
		t.Errorf("busy after done = %v", busy)
	}
	s.resetReqs("t1")
	if len(s.inflight["t1"]) != 0 {
		t.Error("a new document must clear the old page's open requests")
	}
}
