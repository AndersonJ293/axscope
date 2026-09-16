// Text helpers for the reading: normalize, truncate and recognize what is not
// content.
package browser

import (
	"strings"
	"unicode"
)

func repeatOf(text, parent string) bool {
	if text == "" || parent == "" {
		return false
	}
	return strings.Contains(strings.ToLower(parent), strings.ToLower(text))
}

// separatorOnly says whether the text is only layout punctuation ("|", "·",
// "•") — a separator drawn with text, not content.
func separatorOnly(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("|·•/–—»«›<>:;,.()[]{}\u00a0", r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func norm(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// --- text summary of a container ---

// summaryText is how much text the line of a container may summarize.
const summaryText = 220

// containerText returns the text the line of a container may summarize, with the
// nodes that would be consumed. A branch with a target inside is skipped, and a
// summary that does not fit is discarded whole so nothing is consumed.
func (b *snapBuilder) containerText(nodeID string) (string, []string) {
	var parts, owners []string
	for _, cid := range b.children[nodeID] {
		c := b.nodes[cid]
		if c == nil || b.consumed[cid] {
			continue
		}
		role := c.Role.str()
		switch {
		case role == "StaticText" || role == "InlineTextBox":
			t := norm(c.Name.str())
			if t == "" {
				t = norm(c.Value.str())
			}
			if t != "" {
				parts = append(parts, t)
				owners = append(owners, cid)
			}
		case c.Ignored || (role == "generic" && norm(c.Name.str()) == ""):
			if b.hasTarget(cid) {
				continue
			}
			if inner, inside := b.containerText(cid); inner != "" {
				parts = append(parts, inner)
				owners = append(owners, inside...)
			}
		}
	}
	return strings.Join(parts, " "), owners
}

// hasTarget says whether the subtree has an actionable target — that is, whether
// the text in there is an item's label rather than loose summarizable content.
func (b *snapBuilder) hasTarget(nodeID string) bool {
	if v, ok := b.targetCache[nodeID]; ok {
		return v
	}
	v := false
	if n := b.nodes[nodeID]; n != nil {
		v = !n.Ignored && b.eligibleRef(n)
		if !v {
			for _, c := range b.children[nodeID] {
				if b.hasTarget(c) {
					v = true
					break
				}
			}
		}
	}
	b.targetCache[nodeID] = v
	return v
}
