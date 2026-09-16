// Flattened table row: a data row becomes a single line, as long as nothing is
// lost along the way.
package browser

import "strings"

// cellSeparator separates the cells of a flattened table row.
const cellSeparator = " · "

// rowLine summarizes a table row into a single line, returning false when it
// cannot; the check comes before joining, which would consume the nodes.
func (b *snapBuilder) rowLine(nodeID string) (string, bool) {
	var cells []string
	for _, cid := range b.children[nodeID] {
		c := b.nodes[cid]
		if c == nil || c.Ignored {
			continue
		}
		if !cellRoles[c.Role.str()] {
			return "", false
		}
		if !b.canFlatten(cid) {
			return "", false
		}
		cells = append(cells, cid)
	}
	if len(cells) == 0 {
		return "", false
	}

	var vals []string
	for _, cid := range cells {
		c := b.nodes[cid]
		t := norm(c.Name.str())
		if t == "" {
			// The cell has no target inside, so the collection is safe here.
			t, _ = b.containerText(cid)
		}
		// The separator cannot come from inside, or it becomes another cell.
		if strings.Contains(t, cellSeparator) {
			return "", false
		}
		vals = append(vals, t)
	}
	return strings.Join(vals, cellSeparator), true
}

// canFlatten says whether a node can become text inside another row without
// losing anything: no target, no shown property (`[checked]`, `[level=2]`…), and
// no role with structure of its own — image, list and nested table keep their row.
func (b *snapBuilder) canFlatten(nodeID string) bool {
	n := b.nodes[nodeID]
	if n == nil || n.Ignored || skipRoles[n.Role.str()] {
		return true
	}
	role := n.Role.str()
	if b.eligibleRef(n) || b.props(n) != "" {
		return false
	}
	if role != "StaticText" && role != "InlineTextBox" && !textualRoles[role] {
		return false
	}
	for _, c := range b.children[nodeID] {
		if !b.canFlatten(c) {
			return false
		}
	}
	return true
}
