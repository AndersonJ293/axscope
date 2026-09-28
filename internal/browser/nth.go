package browser

import (
	"fmt"
	"strconv"
	"strings"
)

// splitNth takes the Playwright-style suffix off a target: `text=Save >> nth=1`
// is the second of the matches (0-based, like Playwright, whose syntax agents
// already know). n is -1 when there is no suffix.
func splitNth(spec string) (string, int, error) {
	base, suffix, found := strings.Cut(spec, ">>")
	if !found {
		return spec, -1, nil
	}
	raw, ok := strings.CutPrefix(strings.TrimSpace(suffix), "nth=")
	if !ok {
		return spec, -1, fmt.Errorf("%q: the only chain step is `>> nth=N`", spec)
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return spec, -1, fmt.Errorf("%q: nth takes a whole number from 0", spec)
	}
	return strings.TrimSpace(base), n, nil
}

// jsDistinct keeps one element per target from a list of matches, in document
// order: a wrapper whose descendant also matches and accepts the action gives
// way to it (the div around the button), and a match inside another kept match
// is the same target (the span inside the button).
const jsDistinct = `
	const actionableEl = el => el.matches('a,button,input,select,textarea,summary,[role],[tabindex],[contenteditable="true"],[draggable="true"]') || typeof el.onclick === 'function';
	const distinct = list => {
		const wrappers = list.filter(el => !list.some(o => o !== el && el.contains(o) && actionableEl(o)));
		return wrappers.filter(el => !wrappers.some(o => o !== el && o.contains(el)));
	};`

// textGroupExpression lists the distinct targets tied at the best level of the
// text search's preference (exact name, in view, uncovered): the ones the
// search cannot tell apart, which `>> nth=N` picks from.
func textGroupExpression(want string) string {
	return fmt.Sprintf(`(() => {
		const want = %s;
		%s
		%s
		%s
		%s
		%s
		const scored = [];
		for (const el of underShadow(document, sel, [])) {
			const t = text(el);
			if (!t) continue;
			const exact = t === want;
			if (!exact && !t.includes(want)) continue;
			scored.push([el, [exact ? 0 : 1, hidden(el), covered(el)]]);
		}
		if (!scored.length) return [];
		const best = scored.map(s => s[1]).reduce((a, b) => (a[0] - b[0] || a[1] - b[1] || a[2] - b[2]) <= 0 ? a : b);
		const tied = scored.filter(s => s[1][0] === best[0] && s[1][1] === best[1] && s[1][2] === best[2]).map(s => s[0]);
		return distinct(tied);
	})()`, strconv.Quote(want), underShadow, jsCandidates, jsElementText, jsCovered, jsDistinct)
}

// cssGroupExpression lists every match of a selector, light document first,
// shadow roots when it finds nothing — the same order cssExpression uses.
func cssGroupExpression(sel string) string {
	target := strconv.Quote(sel)
	return fmt.Sprintf(`(() => {
		%s
		const light = [...document.querySelectorAll(%s)];
		return light.length ? light : underShadow(document, %s, []);
	})()`, underShadow, target, target)
}

// ambiguityNote is what an action adds when its target matched several
// elements the search could not tell apart: it acted on one, and the agent
// should know, with the way to pick another.
func ambiguityNote(spec string, matches int) string {
	if matches < 2 {
		return ""
	}
	return fmt.Sprintf("%d elements match %s — acted on the first; pick another with `%s >> nth=1` (0-based) or a ref from snap",
		matches, spec, spec)
}
