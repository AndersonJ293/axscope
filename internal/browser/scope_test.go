package browser

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// build is buildText for a test: a scope that fails to resolve is a test bug.
func build(t *testing.T, nodes []axNode, opts SnapshotOptions) *Snapshot {
	t.Helper()
	snap, err := buildText(nodes, opts)
	if err != nil {
		t.Fatalf("buildText: %v", err)
	}
	return snap
}

func withProp(n axNode, name string, value any) axNode {
	raw, _ := json.Marshal(value)
	n.Properties = append(n.Properties, struct {
		Name  string `json:"name"`
		Value struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"value"`
	}{Name: name, Value: struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}{Type: "boolean", Value: raw}})
	return n
}

// modalPage is the Bootstrap docs page with the demo modal open: a long page
// behind, a closed modal (aria-hidden, so ignored) and the open one.
func modalPage() []axNode {
	closed := ax("closed", "root", "dialog", "Other modal", 90)
	closed.Ignored = true
	return []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("nav", "root", "navigation", "Docs", 1),
		ax("l1", "nav", "link", "Introduction", 2),
		ax("l2", "nav", "link", "Download", 3),
		ax("main", "root", "main", "", 4),
		ax("launch", "main", "button", "Launch demo modal", 5),
		withProp(closed, "modal", true),
		ax("cbtn", "closed", "button", "Hidden close", 91),
		withProp(ax("dlg", "root", "dialog", "Modal title", 50), "modal", true),
		ax("close", "dlg", "button", "Close", 51),
		ax("para", "dlg", "paragraph", "", 0),
		ax("body", "para", "StaticText", "Woo-hoo, you're reading this text in a modal!", 0),
		ax("save", "dlg", "button", "Save changes", 52),
	}
}

// An open modal owns the screen: the reading is the dialog alone, and it says
// so, because an agent that does not know the page was left out concludes the
// page is not there.
func TestSnapReadsTheOpenModalAlone(t *testing.T) {
	snap := build(t, modalPage(), SnapshotOptions{})
	if !snap.ScopeAuto || snap.Scope != `dialog "Modal title"` {
		t.Fatalf("scope = %q auto=%v", snap.Scope, snap.ScopeAuto)
	}
	for _, want := range []string{`- dialog "Modal title"`, `button "Close"`, `button "Save changes"`, "Woo-hoo"} {
		if !strings.Contains(snap.Text, want) {
			t.Errorf("modal reading lacks %q:\n%s", want, snap.Text)
		}
	}
	for _, gone := range []string{"Introduction", "Launch demo modal", "Hidden close"} {
		if strings.Contains(snap.Text, gone) {
			t.Errorf("the page behind the modal leaked %q:\n%s", gone, snap.Text)
		}
	}
	if len(snap.Refs) != 2 {
		t.Errorf("refs = %v, want the two dialog buttons", snap.Refs)
	}
}

// --page is the way back to the whole page; a closed modal never scopes.
func TestSnapPageFlagAndClosedModal(t *testing.T) {
	snap := build(t, modalPage(), SnapshotOptions{Page: true})
	if snap.Scope != "" || !strings.Contains(snap.Text, "Launch demo modal") {
		t.Errorf("--page did not read the page: scope=%q\n%s", snap.Scope, snap.Text)
	}

	nodes := modalPage()
	nodes = nodes[:len(nodes)-5] // drop the open dialog: only the closed one is left
	if snap := build(t, nodes, SnapshotOptions{}); snap.Scope != "" {
		t.Errorf("a closed (ignored) modal scoped the reading to %q", snap.Scope)
	}
}

// A non-modal dialog (a popover, a chat widget) does not take the screen.
func TestSnapIgnoresNonModalDialog(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("b", "root", "button", "Page button", 1),
		ax("dlg", "root", "dialog", "Chat", 2),
		ax("x", "dlg", "button", "Send", 3),
	}
	if snap := build(t, nodes, SnapshotOptions{}); snap.Scope != "" || !strings.Contains(snap.Text, "Page button") {
		t.Errorf("non-modal dialog scoped the reading: %q\n%s", snap.Scope, snap.Text)
	}
}

// within= reads the element the agent named, with the element as the top line,
// and beats the automatic modal scope.
func TestSnapWithinReadsTheElement(t *testing.T) {
	snap := build(t, modalPage(), SnapshotOptions{Scope: 1})
	if snap.Scope != `navigation "Docs"` || snap.ScopeAuto {
		t.Fatalf("scope = %q auto=%v", snap.Scope, snap.ScopeAuto)
	}
	if !strings.HasPrefix(snap.Text, `- navigation "Docs"`) || !strings.Contains(snap.Text, "Download") || strings.Contains(snap.Text, "Modal title") {
		t.Errorf("within reading:\n%s", snap.Text)
	}
	if _, err := buildText(modalPage(), SnapshotOptions{Scope: 777}); err == nil {
		t.Error("a scope missing from the tree must be an error, not the whole page")
	}
}

// depth= keeps the top of the tree and turns each cut container into one line
// with a ref to open it and how much it holds.
func TestSnapDepthCutsWithARefToOpen(t *testing.T) {
	snap := build(t, modalPage(), SnapshotOptions{Page: true, MaxDepth: 1})
	if !strings.Contains(snap.Text, `- navigation "Docs" [ref=`) || !strings.Contains(snap.Text, "(2 targets inside)") {
		t.Errorf("cut line missing:\n%s", snap.Text)
	}
	if strings.Contains(snap.Text, "Introduction") {
		t.Errorf("depth=1 read below the top level:\n%s", snap.Text)
	}
	var navRef string
	for ref, backend := range snap.Refs {
		if backend == 1 {
			navRef = ref
		}
	}
	if navRef == "" {
		t.Errorf("the cut navigation has no ref to open: %v", snap.Refs)
	}
}

// A plain div has no tree node of its own: the nodes inside it (queryAXTree)
// are read as the scope, and the header names the element.
func TestSnapWithinPlainContainer(t *testing.T) {
	snap := build(t, modalPage(), SnapshotOptions{Page: true, Scope: 999, ScopeLabel: "div.toolbar",
		scopeNodes: map[string]bool{"launch": true}})
	if snap.Scope != "div.toolbar" || strings.TrimSpace(snap.Text) != `- button "Launch demo modal" [ref=e1]` {
		t.Errorf("scope=%q\n%s", snap.Scope, snap.Text)
	}
}

// --viewport drops a box wholly off screen with what it holds, and counts the
// targets it left out; a box with no layout is walked into.
func TestSnapViewportLeavesOutOffscreen(t *testing.T) {
	snap := build(t, modalPage(), SnapshotOptions{Page: true, offscreen: map[int]bool{1: true}})
	if strings.Contains(snap.Text, "Introduction") || !strings.Contains(snap.Text, "Launch demo modal") {
		t.Errorf("viewport reading:\n%s", snap.Text)
	}
	if snap.Offscreen != 2 {
		t.Errorf("offscreen = %d, want the nav's 2 links", snap.Offscreen)
	}
}

func TestRectIntersects(t *testing.T) {
	win := rect{0, 1000, 800, 600}
	cases := []struct {
		r    rect
		want bool
	}{
		{rect{10, 1100, 50, 20}, true},  // inside
		{rect{10, 990, 50, 20}, true},   // straddles the top edge
		{rect{10, 100, 50, 20}, false},  // above
		{rect{10, 2000, 50, 20}, false}, // below
		{rect{900, 1100, 50, 20}, false},
		{rect{10, 1200, 0, 0}, true}, // zero-size wrapper inside
	}
	for _, c := range cases {
		if got := win.intersects(c.r); got != c.want {
			t.Errorf("intersects(%+v) = %v, want %v", c.r, got, c.want)
		}
	}
}

// An element keeps its number across readings; a new one gets a number above
// every earlier one, so it can never take an old element's ref.
func TestRefsStayStableAcrossReadings(t *testing.T) {
	nodes := modalPage()
	first := build(t, nodes, SnapshotOptions{Page: true, Gen: 1})
	prev := map[int]int{}
	top := 0
	for ref, backend := range first.Refs {
		n, _ := strconv.Atoi(strings.TrimPrefix(strings.Split(ref, "#")[0], "e"))
		prev[backend] = n
		top = max(top, n)
	}
	// A new link enters at the top of the nav, ahead of every old target.
	nodes = append(nodes[:2], append([]axNode{ax("l0", "nav", "link", "New", 7)}, nodes[2:]...)...)
	second := build(t, nodes, SnapshotOptions{Page: true, Gen: 2, PrevRefs: prev})
	for ref, backend := range second.Refs {
		n, _ := strconv.Atoi(strings.TrimPrefix(strings.Split(ref, "#")[0], "e"))
		if backend == 7 {
			if n <= top {
				t.Errorf("the new link took number %d, not above %d", n, top)
			}
			continue
		}
		if n != prev[backend] {
			t.Errorf("backend %d: e%d, want e%d", backend, n, prev[backend])
		}
	}
}

func TestFindBackendTellsThePageFromAFrame(t *testing.T) {
	host := &frameHost{Session: "child"}
	nodes := []axNode{{NodeID: "1", BackendDOMNodeID: 7}, {NodeID: "x0:1", BackendDOMNodeID: 7, frame: host}}
	if n := findBackend(nodes, 7, ""); n == nil || n.NodeID != "1" {
		t.Fatalf("page: got %+v", n)
	}
	if n := findBackend(nodes, 7, "child"); n == nil || n.NodeID != "x0:1" {
		t.Fatalf("frame: got %+v", n)
	}
	if n := findBackend(nodes, 7, "other"); n != nil {
		t.Fatalf("another frame: got %+v", n)
	}
}
