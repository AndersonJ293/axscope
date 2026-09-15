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

// refFor gives the node the next ref, with the reading's generation in the name.
// The generation is what makes an old reading's ref be refused instead of
// silently pointing at another element.
func (b *snapBuilder) refFor(n *axNode) string {
	if !b.eligibleRef(n) {
		return ""
	}
	b.nextRef++
	ref := "e" + strconv.Itoa(b.nextRef)
	if b.gen > 0 {
		ref += "#" + strconv.Itoa(b.gen)
	}
	b.refs[ref] = n.BackendDOMNodeID
	return ref
}

// focusable asks the tree whether the node receives focus — it is what makes a
// focusable element without an interactive role (tabindex, contenteditable)
// become a target.
func (b *snapBuilder) focusable(n *axNode) bool {
	for _, p := range n.Properties {
		if p.Name == "focusable" {
			return rawBool(p.Value.Value)
		}
	}
	return false
}
