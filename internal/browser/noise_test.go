package browser

import (
	"fmt"
	"strings"
	"testing"
)

// A closed <select> with many options is one line with the value and the count
// (a phone code picker was 250 of a real dialog's 285 lines); a short one
// keeps its options, and an open one shows them all.
func TestClosedSelectIsSummarized(t *testing.T) {
	nodes := []axNode{ax("root", "", "RootWebArea", "", 0)}
	cb := withProp(ax("cb", "root", "combobox", "Phone country code", 1), "expanded", false)
	nodes = append(nodes, cb)
	for i := 0; i < 250; i++ {
		nodes = append(nodes, ax(fmt.Sprintf("o%d", i), "cb", "option", fmt.Sprintf("Country %d", i), 100+i))
	}
	short := withProp(ax("lang", "root", "combobox", "Language", 2), "expanded", false)
	nodes = append(nodes, short,
		ax("l1", "lang", "option", "English", 900), ax("l2", "lang", "option", "Português", 901))

	snap := build(t, nodes, SnapshotOptions{})
	if !strings.Contains(snap.Text, `combobox "Phone country code" [ref=e1] [collapsed] (250 options — `+"`select e1 \"<label>\"`"+` picks one)`) {
		t.Errorf("closed select not summarized:\n%s", snap.Text)
	}
	if strings.Contains(snap.Text, "Country 7") {
		t.Error("a summarized select still lists its options")
	}
	if !strings.Contains(snap.Text, `option "Português"`) {
		t.Error("a short select must keep its options")
	}

	nodes[1] = withProp(ax("cb", "root", "combobox", "Phone country code", 1), "expanded", true)
	if open := build(t, nodes, SnapshotOptions{}); !strings.Contains(open.Text, "Country 7") {
		t.Error("an open select must list its options")
	}
}

// A virtualized list's placeholders are counted, not listed.
func TestEmptyListItemsAreCounted(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("list", "root", "list", "", 0),
		ax("i1", "list", "listitem", "", 0),
		ax("a1", "i1", "link", "Lead Software Engineer", 1),
	}
	for i := 0; i < 18; i++ {
		nodes = append(nodes, ax(fmt.Sprintf("e%d", i), "list", "listitem", "", 0))
	}
	snap := build(t, nodes, SnapshotOptions{})
	if strings.Count(snap.Text, "- listitem") != 1 || !strings.Contains(snap.Text, "(18 empty items — not rendered yet; scroll to load them)") {
		t.Errorf("empty items:\n%s", snap.Text)
	}
}

// depth= cuts containers, not a button whose only child is its own label.
func TestDepthDoesNotCutALeafTarget(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("b", "root", "button", "Search", 1),
		ax("bt", "b", "StaticText", "Search", 0),
		ax("nav", "root", "navigation", "Primary", 2),
		ax("l", "nav", "link", "Home", 3),
	}
	snap := build(t, nodes, SnapshotOptions{MaxDepth: 1})
	if strings.Contains(snap.Text, `button "Search" [ref=e1] (`) {
		t.Errorf("a leaf button was cut:\n%s", snap.Text)
	}
	if !strings.Contains(snap.Text, `navigation "Primary" [ref=`) || !strings.Contains(snap.Text, "(1 target inside)") {
		t.Errorf("the container was not cut:\n%s", snap.Text)
	}
}
