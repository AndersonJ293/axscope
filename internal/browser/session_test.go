package browser

import "testing"

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
