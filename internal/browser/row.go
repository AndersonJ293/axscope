// Flattened table row.
//
// A table is content, not noise — but the format was expensive: a data row came
// out as five (the row and the four cells), and in a 60-row table that was 60% of
// the whole reading. Here the row becomes one, as long as nothing is lost along
// the way.
package browser

import "strings"

// cellSeparator separates the cells of a flattened table row.
const cellSeparator = " · "

// rowLine tries to summarize a table row into a single line, returning `false`
// when it cannot — and then the reading comes out as it always did, one line per
// cell.
//
// The check comes before the collection on purpose: joining the text marks the
// nodes as consumed, and there is no going back from that.
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
			// The cell already went through canFlatten — it has no target
			// inside —, so the collection does not bump into an item label.
			t, _ = b.containerText(cid)
		}
		// The separator cannot come from inside: it would become one more cell
		// for whoever reads.
		if strings.Contains(t, cellSeparator) {
			return "", false
		}
		vals = append(vals, t)
	}
	return strings.Join(vals, cellSeparator), true
}

// canFlatten says whether the node can become text inside another row without
// losing anything: no target (otherwise the ref disappears), no property that
// the reading shows (`[checked]`, `[level=2]`…), and no role that carries
// structure of its own — image, list and nested table are still worth a row.
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
