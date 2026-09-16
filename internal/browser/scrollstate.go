// Scroll state read from the DOM, since the accessibility tree does not carry
// scrolling. Only the vertical axis is reported.
package browser

import (
	"context"
	"encoding/json"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// ScrollArea is a scrollable area: who scrolls, how far, and how much fits.
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

// readPageMeta fetches title, URL and the scroll state in one evaluation. It
// fails silently, since a reading without it is still useful.
func readPageMeta(ctx context.Context, client *cdp.Client, session string) pageMeta {
	var meta pageMeta
	raw, err := dom.Eval(ctx, client, session, pageMetaJS)
	if err != nil {
		return meta
	}
	// A failed decode leaves the zero pageMeta, which is still a valid reading.
	_ = json.Unmarshal(raw, &meta)
	return meta
}

// pageMetaJS collects the page state; scrollable areas come out named with a
// short selector usable with `css=`.
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
		// The cheap test comes first: getComputedStyle on every element is
		// expensive, so only elements that overflow pay for it.
		const remainder = el.scrollHeight - el.clientHeight;
		if (remainder <= 1) continue;
		if (!/(auto|scroll|overlay)/.test(getComputedStyle(el).overflowY)) continue;
		total++;
		scrollAreas.push({ target: short(el), pos: Math.round(el.scrollTop), max: remainder });
	}
	// Largest first: the area that scrolls the most usually matters for
	// "scroll to item 777 of 1000".
	scrollAreas.sort((a, b) => b.max - a.max);
	return { title: document.title, url: location.href, page: page, scrollAreas: scrollAreas.slice(0, 8), total: total };
})()`
