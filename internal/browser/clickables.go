// Clickables that the accessibility tree does not mark: a `div` with a click
// handler and `cursor: pointer` becomes neither a line nor a target.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// Clickable is a target that the tree does not mark.
type Clickable struct {
	Selector string `json:"selector"`
	Label    string `json:"label"`
}

// maxClickables caps how many enter the reading; the rest becomes a count.
const maxClickables = 12

// clickablesJS finds containers with no role and gives each a checked selector.
// The criterion is narrow on purpose: only a container with no target inside,
// since a card that wraps buttons is noise.

const clickablesJS = `(() => {
	const interactive = 'button,a,input,select,textarea,[role],[tabindex]';
	const candidates = [...document.querySelectorAll('*')].filter(el => {
		if (getComputedStyle(el).cursor !== 'pointer') return false;
		if (el.matches(interactive)) return false;
		// Inside a target that already exists (the text of a button, for
		// example) it is not a new target: the span of "Liked 20" has an
		// inherited pointer cursor.
		if (el.closest && el.closest(interactive)) return false;
		if (el.querySelector(interactive)) return false;
		if (!el.getClientRects().length) return false;
		return (el.innerText || '').trim() !== '';
	});
	// Only the outermost: cursor is inherited, so the children of a clickable
	// container also look clickable, and listing them all would repeat the same
	// target.
	const inside = new Set(candidates);
	const roots = candidates.filter(el => {
		for (let p = el.parentElement; p; p = p.parentElement) if (inside.has(p)) return false;
		return true;
	});
	const attempt = (el, s) => { try { return document.querySelector(s) === el ? s : ''; } catch (e) { return ''; } };
	const selectorFor = (el) => {
		if (el.id) { const s = attempt(el, '#' + el.id); if (s) return s; }
		const tag = el.tagName.toLowerCase();
		const cls = typeof el.className === 'string' ? el.className.trim().split(/\s+/).filter(Boolean) : [];
		// The data attribute comes before the class, and by itself:
		// div.thread.active bakes the STATE class into the selector, and it
		// breaks as soon as the conversation changes from active to another.
		for (const attr of el.getAttributeNames()) {
			if (!attr.startsWith('data-')) continue;
			const withData = '[' + attr + '="' + el.getAttribute(attr) + '"]';
			const alone = attempt(el, tag + withData);
			if (alone) return alone;
			if (cls.length) {
				const withClass = attempt(el, tag + '.' + cls.join('.') + withData);
				if (withClass) return withClass;
			}
		}
		if (cls.length) { const s = attempt(el, tag + '.' + cls.join('.')); if (s) return s; }
		// Last resort: a path by position, going up to six levels.
		let path = tag, node = el, i = 0;
		while (node.parentElement && i < 6) {
			const siblings = [...node.parentElement.children];
			path = node.tagName.toLowerCase() + ':nth-child(' + (siblings.indexOf(node) + 1) + ')' + (i ? ' > ' + path : '');
			if (attempt(el, path)) return path;
			node = node.parentElement;
			i++;
		}
		return '';
	};
	const items = roots.map(el => ({
		selector: selectorFor(el),
		label: (el.innerText || '').trim().replace(/\s+/g, ' ').slice(0, 80),
	})).filter(c => c.selector);
	return { items: items.slice(0, 40), total: items.length };
})()`

// readClickables runs the pass and returns the targets without a role. It fails
// silently: the reading without them is still a useful reading.
func readClickables(ctx context.Context, client *cdp.Client, session string) ([]Clickable, int) {
	var res struct {
		Items []Clickable `json:"items"`
		Total int         `json:"total"`
	}
	raw, err := dom.Eval(ctx, client, session, clickablesJS)
	if err != nil {
		return nil, 0
	}
	if json.Unmarshal(raw, &res) != nil {
		return nil, 0
	}
	return res.Items, res.Total
}

// clickablesSection builds the block appended to the reading, kept separate so
// it can be tested without a browser.
func clickablesSection(items []Clickable, total int) string {
	if len(items) == 0 {
		return ""
	}
	shown := items
	if len(shown) > maxClickables {
		shown = shown[:maxClickables]
	}
	var b strings.Builder
	b.WriteString("-- clickables without a role in the tree (the reading does not mark them; here is the selector):")
	for _, c := range shown {
		fmt.Fprintf(&b, "\n  %s — %q", c.Selector, c.Label)
	}
	if rest := total - len(shown); rest > 0 {
		fmt.Fprintf(&b, "\n  (+%d)", rest)
	}
	return b.String()
}
