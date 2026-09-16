package browser

import (
	"strings"
	"testing"
)

// ax builds an accessibility-tree node as the CDP delivers it. Only the fields
// that the reading uses matter here.
func ax(nodeID, parentID, role, name string, backend int) axNode {
	return axNode{
		NodeID:           nodeID,
		ParentID:         parentID,
		Role:             axVal{Type: "role", Value: role},
		Name:             axVal{Type: "string", Value: name},
		BackendDOMNodeID: backend,
	}
}

// fixtureTree covers the cuts the code already makes today: footer, skip-
// navigation blocks, echo of the ancestor's name, identical siblings, anonymous
// wrapper and text that is only separators.
func fixtureTree() []axNode {
	return []axNode{
		ax("root", "", "RootWebArea", "", 0),

		ax("home", "root", "link", "Home", 1),

		ax("skip", "root", "link", "Skip to main content", 0),

		ax("footer", "root", "contentinfo", "", 0),
		ax("foottext", "footer", "StaticText", "© 2026", 0),

		// Anonymous wrapper that is not interesting: it vanishes and the text
		// rises.
		ax("wrap", "root", "generic", "", 0),
		ax("hellotext", "wrap", "StaticText", "Hello", 0),

		// Anonymous structural wrapper (paragraph) with no text of its own: it
		// vanishes and the target inside rises in its place.
		ax("anon", "root", "paragraph", "", 0),
		ax("buylink", "anon", "link", "Buy", 5),

		// Text that is only separators: it is not content.
		ax("sep", "root", "StaticText", "· · ·", 0),

		// Container with no target, no text and no child: the line vanishes.
		ax("empty", "root", "region", "", 0),

		// Identical siblings: the same target offered twice, the first one
		// stays.
		ax("group", "root", "region", "Group", 0),
		ax("same1", "group", "link", "Same", 20),
		ax("same2", "group", "link", "Same", 21),

		// Echo of the ancestor's name via collected text (repeatOf).
		ax("echolink", "root", "link", "Docs", 12),
		ax("echopara", "echolink", "paragraph", "", 0),
		ax("echotext", "echopara", "StaticText", "Docs", 0),

		// A wrapper that repeats the parent's name vanishes from the childIDs.
		ax("xlink", "root", "link", "X", 10),
		ax("xwrap", "xlink", "generic", "X", 0),
		ax("xtext", "xwrap", "StaticText", "X", 0),
	}
}

func TestBuildText_CutsChromeAndNoise(t *testing.T) {
	snap := buildText(fixtureTree(), SnapshotOptions{})

	expected := `- link "Home" [ref=e1]
- text: Hello
- link "Buy" [ref=e2]
- region "Group"
  - link "Same" [ref=e3] (+1 same)
- link "Docs" [ref=e4]
- link "X" [ref=e5]`
	if snap.Text != expected {
		t.Errorf("text diverged:\n--- got ---\n%s\n--- expected ---\n%s", snap.Text, expected)
	}
	if snap.Count != 7 {
		t.Errorf("Count = %d, expected 7", snap.Count)
	}

	refs := map[string]int{"e1": 1, "e2": 5, "e3": 20, "e4": 12, "e5": 10}
	if len(snap.Refs) != len(refs) {
		t.Fatalf("Refs = %v, expected %v", snap.Refs, refs)
	}
	for k, v := range refs {
		if snap.Refs[k] != v {
			t.Errorf("Refs[%q] = %d, expected %d", k, snap.Refs[k], v)
		}
	}
}

// With All, the chrome cut is turned off: footer and skip-link reappear.
func TestBuildText_AllDisablesCut(t *testing.T) {
	snap := buildText(fixtureTree(), SnapshotOptions{All: true})

	expected := `- link "Home" [ref=e1]
- link "Skip to main content"
- contentinfo: © 2026
- text: Hello
- link "Buy" [ref=e2]
- region "Group"
  - link "Same" [ref=e3] (+1 same)
- link "Docs" [ref=e4]
- link "X" [ref=e5]`
	if snap.Text != expected {
		t.Errorf("text diverged:\n--- got ---\n%s\n--- expected ---\n%s", snap.Text, expected)
	}
}

// The reading's generation enters the ref, so that an old ref is refused later.
func TestBuildText_GenerationInRef(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("b", "root", "button", "Send", 7),
	}
	snap := buildText(nodes, SnapshotOptions{Gen: 9})
	if snap.Text != `- button "Send" [ref=e1#9]` {
		t.Errorf("text = %q", snap.Text)
	}
	if snap.Refs["e1#9"] != 7 {
		t.Errorf("Refs = %v", snap.Refs)
	}
}

// A table row whose content is only text becomes a single line; with a target
// inside it stays expanded (one line per cell, which carries the ref), and a role
// with structure of its own — image, nested table — is not flattened.
func TestBuildText_FlattensTableRow(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("tab", "root", "table", "", 0),

		ax("r1", "tab", "row", "", 0),
		ax("c11", "r1", "cell", "1", 0),
		ax("c12", "r1", "cell", "Salvador", 0),

		ax("r2", "tab", "row", "", 0),
		ax("c21", "r2", "cell", "", 0),
		ax("c21t", "c21", "StaticText", "2", 0),
		ax("c22", "r2", "cell", "", 0),
		ax("c22a", "c22", "link", "Recife", 9),

		ax("r3", "tab", "row", "", 0),
		ax("c31", "r3", "cell", "", 0),
		ax("c31i", "c31", "img", "Cover", 0),
	}

	snap := buildText(nodes, SnapshotOptions{})
	expected := `- table
  - row: 1 · Salvador
  - row
    - cell: 2
    - cell
      - link "Recife" [ref=e1]
  - row
    - cell
      - img "Cover"`
	if snap.Text != expected {
		t.Errorf("text diverged:\n--- got ---\n%s\n--- expected ---\n%s", snap.Text, expected)
	}
}

// In RefsOnly the flattened row does not appear: it has no target, and the
// mode's promise is to list only what can be acted on.
func TestBuildText_FlatteningDoesNotLeakInRefsOnly(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("tab", "root", "table", "", 0),
		ax("r1", "tab", "row", "", 0),
		ax("c11", "r1", "cell", "1", 0),
		ax("c12", "r1", "cell", "Salvador", 0),
	}
	snap := buildText(nodes, SnapshotOptions{RefsOnly: true})
	if snap.Text != "" {
		t.Errorf("text = %q, expected empty", snap.Text)
	}
}

// A container's summary must not swallow an item's label: the "Candidate 413"
// text next to an "Open" button would be consumed and the button left ownerless.
func TestBuildText_DoesNotStealItemLabel(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("main", "root", "main", "", 0),
		ax("row", "main", "generic", "", 0),
		ax("label", "row", "generic", "Candidate 413", 0),
		ax("open", "row", "button", "Open", 42),
	}
	snap := buildText(nodes, SnapshotOptions{})

	expected := `- main
  - generic "Candidate 413"
  - button "Open" [ref=e1]`
	if snap.Text != expected {
		t.Errorf("text diverged:\n--- got ---\n%s\n--- expected ---\n%s", snap.Text, expected)
	}
}

// A summary that does not fit is not a summary: it must not consume the nodes it
// gathered, or the rest vanishes from the reading without appearing anywhere.
func TestBuildText_SummaryThatDoesNotFitDoesNotConsume(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("reg", "root", "region", "", 0),
		ax("g", "reg", "generic", "", 0),
		ax("t1", "g", "StaticText", strings.Repeat("a", 200), 0),
		ax("t2", "g", "StaticText", strings.Repeat("b", 200), 0),
	}
	snap := buildText(nodes, SnapshotOptions{})

	if strings.Contains(snap.Text, "- region: ") {
		t.Errorf("summarized what did not fit:\n%s", snap.Text)
	}
	for _, wanted := range []string{strings.Repeat("a", 200), strings.Repeat("b", 200)} {
		if !strings.Contains(snap.Text, wanted) {
			t.Errorf("text vanished from the reading:\n%s", snap.Text)
		}
	}
}

// RefsOnly lists only the actionable targets, without loose text.
func TestBuildText_RefsOnly(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("txt", "root", "StaticText", "loose text", 0),
		ax("b", "root", "button", "Send", 3),
	}
	snap := buildText(nodes, SnapshotOptions{RefsOnly: true})
	if snap.Text != `- button "Send" [ref=e1]` {
		t.Errorf("text = %q", snap.Text)
	}
}

// Without MaxNodes the default ceiling is 1500; with a low ceiling the reading
// truncates. The names are distinct on purpose: equal names would collapse into a
// single line and leave nothing to truncate.
func TestBuildText_Truncates(t *testing.T) {
	nodes := []axNode{ax("root", "", "RootWebArea", "", 0)}
	for i := 0; i < 5; i++ {
		nodes = append(nodes, ax("t"+string(rune('a'+i)), "root", "button", "b"+string(rune('a'+i)), i+1))
	}
	snap := buildText(nodes, SnapshotOptions{MaxNodes: 2})
	if !snap.Truncated {
		t.Fatalf("expected Truncated; text=%q", snap.Text)
	}
	if snap.Count != 2 {
		t.Errorf("Count = %d, expected 2", snap.Count)
	}
}

// A named landmark whose child only repeats the name cannot vanish: the child is
// suppressed as an echo, but pruning a line with a name would lose the landmark.
func TestBuildText_NamedLandmarkSurvivesWithoutChildren(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("top", "root", "banner", "Top", 0),
		ax("toptext", "top", "StaticText", "Top", 0),
		ax("nav", "root", "navigation", "", 0),
	}
	snap := buildText(nodes, SnapshotOptions{})
	if snap.Text != `- banner "Top"` {
		t.Errorf("text = %q, expected the named banner and nothing from the unnamed navigation", snap.Text)
	}
}

// The identical-sibling cut must also hold at the root, or two identical children
// of the root both appear.
func TestBuildText_DedupeAtRoot(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("s1", "root", "link", "Same", 1),
		ax("s2", "root", "link", "Same", 2),
	}
	snap := buildText(nodes, SnapshotOptions{})
	if snap.Text != `- link "Same" [ref=e1] (+1 same)` {
		t.Errorf("text = %q", snap.Text)
	}
}
