package browser

import (
	"strconv"
	"strings"
	"testing"
)

// A node grafted from a cross-origin frame yields a FrameRef, never a page ref:
// its backend id is the frame's and would resolve to another element (or none)
// in the page's session.
func TestFrameNodesYieldFrameRefs(t *testing.T) {
	host := &frameHost{Session: "S2", Owner: 7}
	btn := ax("f0:b", "ifr", "button", "Pay", 5)
	btn.frame = host
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("page", "root", "button", "Outer", 5),
		ax("ifr", "root", "Iframe", "", 7),
		btn,
	}
	snap := build(t, nodes, SnapshotOptions{Gen: 2})
	if len(snap.Refs) != 1 || len(snap.FrameRefs) != 1 {
		t.Fatalf("refs=%v frameRefs=%v", snap.Refs, snap.FrameRefs)
	}
	for ref, fr := range snap.FrameRefs {
		if fr != (FrameRef{Session: "S2", Backend: 5, Owner: 7}) {
			t.Errorf("%s = %+v", ref, fr)
		}
		if _, clash := snap.Refs[ref]; clash {
			t.Errorf("%s is both a page and a frame ref", ref)
		}
	}
}

// A frame element keeps its number across readings, and a new one — in the page
// or in the frame — gets a number above every earlier one.
func TestFrameRefsStayStable(t *testing.T) {
	host := &frameHost{Session: "S2", Owner: 7}
	mk := func(id, name string, backend int) axNode {
		n := ax(id, "ifr", "button", name, backend)
		n.frame = host
		return n
	}
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("page", "root", "button", "Outer", 5),
		ax("ifr", "root", "Iframe", "", 7),
		mk("f0:pay", "Pay", 5),
	}
	first := build(t, nodes, SnapshotOptions{Gen: 1})
	prevPage, prevFrame := map[int]int{}, map[string]int{}
	top := 0
	num := func(ref string) int {
		n, _ := strconv.Atoi(strings.TrimPrefix(strings.Split(ref, "#")[0], "e"))
		return n
	}
	for ref, b := range first.Refs {
		prevPage[b] = num(ref)
		top = max(top, num(ref))
	}
	payNum := 0
	for ref, fr := range first.FrameRefs {
		prevFrame[fr.Key()] = num(ref)
		payNum = num(ref)
		top = max(top, num(ref))
	}
	// A new button enters the frame ahead of Pay.
	nodes = append(nodes[:3], mk("f0:new", "New", 9), nodes[3])
	second := build(t, nodes, SnapshotOptions{Gen: 2, PrevRefs: prevPage, PrevFrameRefs: prevFrame})
	for ref, fr := range second.FrameRefs {
		switch fr.Backend {
		case 5:
			if num(ref) != payNum {
				t.Errorf("Pay moved from e%d to %s", payNum, ref)
			}
		case 9:
			if num(ref) <= top {
				t.Errorf("the new frame button took e%d, not above %d", num(ref), top)
			}
		}
	}
}
