// Scroll state, read from the DOM.
//
// The accessibility tree does not carry scrolling: the CDP does not serialize
// scrollTop/scrollHeight in it. Without that the reading shows the lines and
// never where one is in them — "scroll to item 777 of 1000" becomes a guess. The
// information comes in a pass over the DOM, together with title and URL, so it
// does not cost an extra round trip: before it was two evaluations, now it is
// one.
//
// Only the vertical axis enters. The horizontal one exists (code bar, wide panel)
// and today stays out: reporting both would invent a format for a case that has
// not bitten yet. When it bites, the place is here.
package browser

import (
	"context"
	"encoding/json"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// ScrollArea is where a scrollable area is: who scrolls, how much has already
// scrolled and how much still fits.
type ScrollArea struct {
	Name string `json:"target"`
	Pos  int    `json:"pos"`
	Max  int    `json:"max"`
}

// pageMeta is what the reading needs to know about the page beyond the tree.
type pageMeta struct {
	Title       string       `json:"title"`
	URL         string       `json:"url"`
	Page        *ScrollArea  `json:"page"`
	ScrollAreas []ScrollArea `json:"scrollAreas"`
	Total       int          `json:"total"`
}

// readPageMeta fetches title, URL and the scroll state in a single evaluation.
// It fails silently: a reading without that is still a useful reading — what
// cannot happen is the screen not being read because the page did not let the
// scroll be measured.
func readPageMeta(ctx context.Context, client *cdp.Client, session string) pageMeta {
	var meta pageMeta
	raw, err := dom.Eval(ctx, client, session, pageMetaJS)
	if err != nil {
		return meta
	}
	_ = json.Unmarshal(raw, &meta)
	return meta
}

// pageMetaJS collects the page state. The scrollable areas come out named with a
// short selector, so the agent can aim at them by `css=` without translation.
const pageMetaJS = `(() => {
	const short = (el) => {
		if (el.id) return '#' + el.id;
		const tag = (el.tagName || '?').toLowerCase();
		const cls = [...(el.classList || [])].slice(0, 2);
		return cls.length ? tag + '.' + cls.join('.') : tag;
	};
	const doc = document.scrollingElement || document.documentElement;
	const pageRemainder = Math.round(doc.scrollHeight - doc.clientHeight);
	const page = { target: 'page', pos: Math.round(doc.scrollTop), max: pageRemainder };
	const scrollAreas = [];
	let total = pageRemainder > 1 ? 1 : 0;
	for (const el of document.querySelectorAll('*')) {
		if (el === doc || el === document.documentElement || el === document.body) continue;
		// The cheap test comes first: almost every element does not scroll, and
		// getComputedStyle on all of them is expensive. Only the survivors pay.
		const remainder = el.scrollHeight - el.clientHeight;
		if (remainder <= 1) continue;
		if (!/(auto|scroll|overlay)/.test(getComputedStyle(el).overflowY)) continue;
		total++;
		scrollAreas.push({ target: short(el), pos: Math.round(el.scrollTop), max: remainder });
	}
	// Largest first: the area that scrolls the most is the one that usually
	// matters for "scroll to item 777 of 1000", and the header is short by
	// definition.
	scrollAreas.sort((a, b) => b.max - a.max);
	return { title: document.title, url: location.href, page: page, scrollAreas: scrollAreas.slice(0, 8), total: total };
})()`
