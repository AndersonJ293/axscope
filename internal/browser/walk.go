// The walk through the tree: what becomes a line, what vanishes and what is
// summarized.
package browser

import (
	"fmt"
	"strconv"
	"strings"
)

func (b *snapBuilder) walk(nodeID string, depth int, parentName string) {
	n := b.nodes[nodeID]
	if n == nil || b.consumed[nodeID] {
		return
	}
	if n.Ignored {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentName)
		}
		return
	}

	role := n.Role.str()
	name := norm(n.Name.str())

	if skipRoles[role] {
		return
	}

	// Page chrome: footer and skip-navigation blocks. Nobody acts on them.
	if !b.all {
		if role == "contentinfo" || noiseNameRe.MatchString(name) {
			return
		}
	}

	// A table row whose content is only text fits in a single line. A table is
	// content, not noise: what weighs is the format — one line per cell costs
	// five lines per data row (measured: 305 of the 508 reading lines in a
	// 60-row table). With a target inside, the expansion stays, because it is
	// what carries the ref.
	if role == "row" && name == "" && !b.refsOnly {
		if compact, ok := b.rowLine(nodeID); ok {
			b.emit(depth, "- row: "+compact)
			return
		}
	}

	if role == "StaticText" || role == "InlineTextBox" {
		if parentName == "" && !b.refsOnly {
			text := norm(n.Name.str())
			if text == "" {
				text = norm(n.Value.str())
			}
			if text != "" && !separatorOnly(text) {
				b.emit(depth, "- text: "+truncate(text, 220))
			}
		}
		return
	}

	// Layout containers vanish; the children inherit the depth.
	if layoutRoles[role] {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentName)
		}
		return
	}

	// An image inside an already named target (a link with an image) is
	// redundant.
	if (role == "img" || role == "image") && parentName != "" {
		return
	}

	interesting := interactiveRoles[role] || structuralRoles[role] || name != ""

	if !interesting {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentName)
		}
		return
	}

	ref := b.refFor(n)
	if b.refsOnly && ref == "" {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentName)
		}
		return
	}

	line := "- "
	if role == "" || role == "generic" || role == "none" {
		line += "generic"
	} else {
		line += role
	}
	hadText := false
	if name != "" {
		line += " " + strconv.Quote(name)
	} else if !b.refsOnly && !frameRoles[role] {
		// The iframe stays out of the collection: it has no text of its own,
		// and what the collection would fish is the text of the inner document
		// — which already appears right below, in the tree that was grafted
		// onto it.
		if text, owners := b.containerText(nodeID); text != "" && len(text) <= summaryText && !repeatOf(text, parentName) {
			line += ": " + text
			hadText = true
			for _, d := range owners {
				b.consumed[d] = true
			}
		}
	}

	// Anonymous wrapper (no name, no text of its own, no target): it does not
	// become a line — it vanishes, and the children rise in its place.
	if name == "" && !hadText && ref == "" && anonRoles[role] {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentName)
		}
		return
	}

	if ref != "" {
		line += " [ref=" + ref + "]"
	}
	line += b.props(n)
	b.emit(depth, line)
	before := len(b.out)

	// The node's name descends as context: a child that only repeats it is not
	// said again.
	b.walkChildren(nodeID, name, depth+1, parentName)

	// A container that yielded no child line drops out — but only scaffolding,
	// and only when it itself said nothing. A line with a name says content (it
	// is the case of the "Candidate 413" label, whose text child is suppressed
	// as an echo of the parent's name): erasing it was erasing the whole label.
	if ref == "" && name == "" && !hadText && len(b.out) == before {
		if scaffoldRoles[role] || landmarkRoles[role] {
			b.out = b.out[:len(b.out)-1]
		}
	}
}

// walkChildren walks the children at `depth`, summarizing identical siblings
// (same role and name) into a single line.
//
// It is a method, and not a loose loop inside walk, so that it also holds at the
// root level — before, the seen map only came to life in there, and two
// identical targets that were children of the root both appeared.
//
// The first sibling stays and the others become a count on its line. Deleting in
// silence would hide a target: two buttons with the same label are different
// nodes, at different positions. This way the agent knows there is more than one
// — and reaches the other by css/pos.

func (b *snapBuilder) walkChildren(nodeID, name string, depth int, parentName string) {
	childParent := parentName
	if name != "" {
		childParent = name
	}
	ids := b.childIDs(nodeID, name)

	keys := make([]string, len(ids))
	count := map[string]int{}
	for i, cid := range ids {
		c := b.nodes[cid]
		if c == nil || c.Ignored {
			continue
		}
		cn := norm(c.Name.str())
		if cn == "" || !b.eligibleRef(c) {
			continue
		}
		k := c.Role.str() + "\x00" + cn
		keys[i] = k
		count[k]++
	}

	seen := map[string]bool{}
	for i, cid := range ids {
		k := keys[i]
		if k != "" {
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		mark := len(b.out)
		b.walk(cid, depth, childParent)
		// The child's own line is the first one it emits, and the tree is read
		// from top to bottom.
		if k != "" && count[k] > 1 && len(b.out) > mark {
			b.out[mark] += fmt.Sprintf(" (+%d same)", count[k]-1)
		}
	}
}

// childIDs returns the children to walk, skipping wrappers that only repeat the
// parent's name — the classic `link "X" > generic "X" > paragraph: X`. The
// target is already said; the grandchildren rise into the wrapper's place.

func (b *snapBuilder) childIDs(nodeID, name string) []string {
	var out []string
	for _, cid := range b.children[nodeID] {
		c := b.nodes[cid]
		if c == nil {
			continue
		}
		if name != "" && !c.Ignored && norm(c.Name.str()) == name && !b.eligibleRef(c) {
			out = append(out, b.childIDs(cid, name)...)
			continue
		}
		out = append(out, cid)
	}
	return out
}

// repeatOf says whether a text is only an echo of the ancestor's name (already
// said above).

func (b *snapBuilder) emit(depth int, line string) {
	if len(b.out) >= b.max {
		b.truncated = true
		return
	}
	b.out = append(b.out, strings.Repeat("  ", depth)+line)
}
