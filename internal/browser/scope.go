// Scoped reading: the part of the tree a snap starts from — an element the agent
// named, or the modal dialog that owns the screen.
package browser

import "fmt"

// dialogRoles are the roles a modal can carry in the tree.
var dialogRoles = map[string]bool{"dialog": true, "alertdialog": true}

// findModal returns the open modal dialog, if any: role dialog/alertdialog,
// not ignored, and marked modal (aria-modal=true or <dialog>.showModal()). A
// closed Bootstrap modal is aria-hidden and ignored, so it never matches. With
// several, the last in document order is the one on top.
func findModal(nodes []axNode) *axNode {
	var found *axNode
	for i := range nodes {
		n := &nodes[i]
		if n.Ignored || !dialogRoles[n.Role.str()] {
			continue
		}
		if boolProp(n, "modal") {
			found = n
		}
	}
	return found
}

// findBackend returns the tree node for a DOM node. An element the tree drops
// (a plain div) has no node of its own, so its nearest descendants that do are
// not reachable from here — the caller says so rather than reading the page.
func findBackend(nodes []axNode, backend int) *axNode {
	for i := range nodes {
		if nodes[i].BackendDOMNodeID == backend {
			return &nodes[i]
		}
	}
	return nil
}

func boolProp(n *axNode, name string) bool {
	for _, p := range n.Properties {
		if p.Name == name {
			return rawBool(p.Value.Value)
		}
	}
	return false
}

// scopeLabel names the scope for the header: role and name, as a line would.
func scopeLabel(n *axNode) string {
	role := n.Role.str()
	if role == "" || role == "none" {
		role = "generic"
	}
	if name := norm(n.Name.str()); name != "" {
		return fmt.Sprintf("%s %q", role, truncate(name, 60))
	}
	return role
}

// countTargets counts the refs a subtree would yield, for the line that stands
// in for it when depth= cuts the reading.
func (b *snapBuilder) countTargets(nodeID string) int {
	total := 0
	for _, c := range b.children[nodeID] {
		n := b.nodes[c]
		if n == nil {
			continue
		}
		if !n.Ignored && b.eligibleRef(n) {
			total++
		}
		total += b.countTargets(c)
	}
	return total
}

// scopeRef gives a container a ref even without an interactive role, so the
// line depth= cuts at can be opened with `snap within=<ref>`.
func (b *snapBuilder) scopeRef(n *axNode) string {
	if n.BackendDOMNodeID == 0 {
		return ""
	}
	return b.nameNode(n)
}
