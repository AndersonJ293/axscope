package agent

import (
	"strings"
	"testing"

	"github.com/AndersonJ293/axscope/internal/browser"
)

// Only a change is said: nothing changed is no line at all, since each line is
// context on every action.
func TestChangesSaysOnlyWhatChanged(t *testing.T) {
	tabs := []browser.TabInfo{{Index: 1, TargetID: "T1", Active: true}}
	same := &pageState{Title: "Jobs", tabs: map[string]bool{"T1": true}}
	if got := changes(same, &pageState{Title: "Jobs"}, tabs); len(got) != 0 {
		t.Errorf("no change, but: %v", got)
	}

	opened := changes(same, &pageState{Title: "Jobs", Dialog: "Apply"}, tabs)
	if len(opened) != 1 || opened[0] != `dialog opened: "Apply"` {
		t.Errorf("opened = %v", opened)
	}
	closed := changes(&pageState{Title: "Jobs", Dialog: "Apply", tabs: same.tabs}, &pageState{Title: "Jobs"}, tabs)
	if len(closed) != 1 || closed[0] != `dialog closed: "Apply"` {
		t.Errorf("closed = %v", closed)
	}

	withTab := append(tabs, browser.TabInfo{Index: 2, TargetID: "T2", URL: "https://a/next", Active: true})
	got := strings.Join(changes(same, &pageState{Title: "Step 2"}, withTab), "\n")
	for _, want := range []string{"new tab: [2] https://a/next (now the active tab)", `title: "Jobs" → "Step 2"`} {
		if !strings.Contains(got, want) {
			t.Errorf("changes lack %q:\n%s", want, got)
		}
	}
	if changes(nil, same, tabs) != nil {
		t.Error("without a before, nothing can be said")
	}
}
