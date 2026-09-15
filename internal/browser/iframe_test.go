package browser

import "testing"

// Regression (mission 13): the reading showed the iframe as a single line — the
// inner document is another accessibility tree, and without joining the two the
// agent never learned that a button exists there.
func TestGraftFrame_PutsContentInsideIframe(t *testing.T) {
	parent := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("frame", "root", "Iframe", "", 42),
	}
	// The frame's tree, as the CDP delivers it: numbered from its own root and
	// with its document's name.
	child := []axNode{
		ax("root", "", "RootWebArea", "Inner document", 0),
		ax("h", "root", "heading", "Iframe zone", 0),
		ax("b", "root", "button", "Click inside", 43),
	}

	joined := append(parent, graftFrame(child, "f0:", "frame")...)
	snap := buildText(joined, SnapshotOptions{})

	// The frame root does not become a line: `RootWebArea "Inner document"`
	// would be noise inside the iframe, and what matters is the content.
	expected := `- Iframe
  - heading "Iframe zone"
  - button "Click inside" [ref=e1]`
	if snap.Text != expected {
		t.Errorf("text diverged:\n--- got ---\n%s\n--- expected ---\n%s", snap.Text, expected)
	}
}

// The cost of going to fetch the frame trees stays with whoever has an iframe.
func TestHasIframe(t *testing.T) {
	if hasIframe([]axNode{ax("a", "", "button", "x", 1)}) {
		t.Error("with no iframe in the tree, it should not go fetch frames")
	}
	if !hasIframe([]axNode{ax("a", "", "Iframe", "", 1)}) {
		t.Error("with an iframe, it has to join")
	}
}
