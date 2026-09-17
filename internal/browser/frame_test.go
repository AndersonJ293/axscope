package browser

import (
	"strings"
	"testing"
)

// A frame whose tree did not graft (cross-origin/OOPIF, or still loading) must
// say so: a bare "- Iframe" reads as an empty frame and is what sent a real
// session to raw JS in a shadow root when the frame was the actual blocker.
func TestUnreachableFrameIsNamed(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("fr", "root", "Iframe", "", 7),
	}
	got := buildText(nodes, SnapshotOptions{}).Text
	if !strings.Contains(got, "cross-origin") {
		t.Errorf("the unreachable frame was not named: %q", got)
	}

	// A frame whose content grafted (it has children) does not carry the note.
	nodes = append(nodes, ax("inside", "fr", "heading", "Inside", 0))
	if got := buildText(nodes, SnapshotOptions{}).Text; strings.Contains(got, "cross-origin") {
		t.Errorf("a grafted frame must not carry the note: %q", got)
	}
}
