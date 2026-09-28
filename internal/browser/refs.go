// Ref assignment: who can receive one, and with what number.
package browser

import "strconv"

// eligibleRef says whether the node can receive a ref, without consuming
// numbering.
func (b *snapBuilder) eligibleRef(n *axNode) bool {
	if n.BackendDOMNodeID == 0 {
		return false
	}
	role := n.Role.str()
	return interactiveRoles[role] || b.focusable(n)
}

// refFor gives the node the next ref, with the reading's generation in the name,
// so an old reading's ref is refused instead of silently pointing elsewhere.
func (b *snapBuilder) refFor(n *axNode) string {
	if !b.eligibleRef(n) {
		return ""
	}
	return b.nameNode(n)
}

// nameNode numbers a node. A node of a cross-origin frame gets a fresh number
// and goes to frameRefs: its backend id is the frame's, so it can neither reuse
// a page element's number nor resolve in the page's session.
func (b *snapBuilder) nameNode(n *axNode) string {
	if n.frame == nil {
		return b.name(n.BackendDOMNodeID)
	}
	fr := FrameRef{Session: n.frame.Session, Backend: n.BackendDOMNodeID, Owner: n.frame.Owner}
	num := b.prevFrame[fr.Key()]
	if num == 0 || b.used[num] {
		b.nextRef++
		num = b.nextRef
	}
	b.used[num] = true
	ref := "e" + strconv.Itoa(num)
	if b.gen > 0 {
		ref += "#" + strconv.Itoa(b.gen)
	}
	b.frameRefs[ref] = fr
	return ref
}

// Key names a frame element across readings: its frame's session and its
// backend id there.
func (f FrameRef) Key() string { return f.Session + ":" + strconv.Itoa(f.Backend) }

// name numbers a node: the number it had in the previous reading when it had
// one, a fresh one (above every earlier number) otherwise.
func (b *snapBuilder) name(backend int) string {
	num := b.prev[backend]
	if num == 0 || b.used[num] {
		b.nextRef++
		num = b.nextRef
	}
	b.used[num] = true
	ref := "e" + strconv.Itoa(num)
	if b.gen > 0 {
		ref += "#" + strconv.Itoa(b.gen)
	}
	b.refs[ref] = backend
	return ref
}

// focusable asks the tree whether the node receives focus, making a tabindex or
// contenteditable element a target without an interactive role.
func (b *snapBuilder) focusable(n *axNode) bool {
	for _, p := range n.Properties {
		if p.Name == "focusable" {
			return rawBool(p.Value.Value)
		}
	}
	return false
}
