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
		inflight:     map[string]map[string]string{"sid": {}},
		lastActivity: map[string]time.Time{"sid": time.Now().Add(-time.Second)},
	}
	if pending, idle := quiet.WaitForNetworkIdle(context.Background(), "sid", 50*time.Millisecond, time.Second); !idle || len(pending) != 0 {
		t.Errorf("idle network: pending=%v idle=%v, want empty/true", pending, idle)
	}

	busy := &Session{
		inflight: map[string]map[string]string{"sid": {
			"r1": "https://a/one",
			"r2": "https://a/two",
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
