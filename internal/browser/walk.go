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

	// A table row whose content is only text fits in a single line — one line per
	// cell costs far more for a data table. With a target inside, the expansion
	// stays, because it is what carries the ref.
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

	// An image inside an already named target (a link with an image) is redundant.
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
		// The iframe stays out: it has no text of its own, and its inner text
		// already appears below in the tree grafted onto it.
		if text, owners := b.containerText(nodeID); text != "" && len(text) <= summaryText && !repeatOf(text, parentName) {
			line += ": " + text
			hadText = true
			for _, d := range owners {
				b.consumed[d] = true
			}
		}
	}

	// Anonymous wrapper (no name, no text, no target) vanishes; children rise.
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
	if frameRoles[role] && len(b.children[nodeID]) == 0 {
		// The frame's tree did not graft onto it: it is cross-origin (its own
		// process, out of reach) or still loading. Naming it beats a bare
		// "- Iframe", which reads as an empty frame.
		line += " (its content is not in the tree: a cross-origin frame or one still loading)"
	}
	b.emit(depth, line)
	before := len(b.out)

	// The node's name descends as context: a child that only repeats it is not
	// said again.
	b.walkChildren(nodeID, name, depth+1, parentName)

	// A container that yielded no child line drops out, but only scaffolding and
	// only when it said nothing itself: a line with a name is content and stays.
	if ref == "" && name == "" && !hadText && len(b.out) == before {
		if scaffoldRoles[role] || landmarkRoles[role] {
			b.out = b.out[:len(b.out)-1]
		}
	}
}

// walkChildren walks the children at `depth`, summarizing identical siblings
// (same role and name) into a single line. Deleting in silence would hide a
// target, so the first sibling stays and the others become a count.
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
// parent's name; their grandchildren rise into the wrapper's place.
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

func (b *snapBuilder) emit(depth int, line string) {
	if len(b.out) >= b.max {
		b.truncated = true
		return
	}
	b.out = append(b.out, strings.Repeat("  ", depth)+line)
}
