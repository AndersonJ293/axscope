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
	return b.name(n.BackendDOMNodeID)
}

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
