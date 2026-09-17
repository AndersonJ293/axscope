package browser

import "testing"

// A tab the page opens itself becomes active; the tabs known when the session was
// built do not, because the discover call re-announces every open one.
func TestPageOpenedActivatesOnlyNewTabs(t *testing.T) {
	s := &Session{
		tabs:    map[string]*Tab{"old": {TargetID: "old", ready: make(chan struct{})}},
		bootSet: map[string]bool{"old": true},
		booted:  true,
		active:  "old",
	}
	if _, created := s.rememberNew("old", "u", "t"); created {
		t.Error("re-remembering a known tab must not report it as new")
	}
	if s.active != "old" {
		t.Errorf("a known tab must not steal the active tab: %q", s.active)
	}
	if _, created := s.rememberNew("new", "u", "t"); !created {
		t.Fatal("a fresh target must be reported as new")
	}
	s.pageOpened("new")
	if s.active != "new" {
		t.Errorf("pageOpened did not activate the tab the page opened: %q", s.active)
	}

	// Before boot there is no "what existed" to compare against, so nothing moves.
	pre := &Session{tabs: make(map[string]*Tab), bootSet: map[string]bool{}, active: "x"}
	pre.pageOpened("y")
	if pre.active != "x" {
		t.Errorf("pageOpened before boot must not switch the active tab: %q", pre.active)
	}
}
