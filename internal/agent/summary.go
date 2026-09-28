package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// pageState is what an action's answer compares before and after, to say what
// the action did beyond "ok": a dialog that opened, a title that changed, a tab
// that appeared. Each line costs context, so only a change is said.
type pageState struct {
	Title  string `json:"title"`
	Dialog string `json:"dialog"`
	tabs   map[string]bool
}

// summaryCommands are the actions whose answer carries the summary.
var summaryCommands = map[string]bool{
	"click": true, "hover": true, "fill": true, "type": true, "press": true,
	"select": true, "check": true, "uncheck": true, "drag": true, "upload": true,
	"download": true,
}

// pageStateJS reads the title and the dialog on top, if any: a modal first
// (aria-modal, <dialog> opened as modal), then any visible dialog role. The
// label is what the snap would call it.
const pageStateJS = `(() => {
	const visible = el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== 'hidden'; };
	const label = el => {
		const aria = (el.getAttribute('aria-label') || '').trim();
		if (aria) return aria;
		const by = el.getAttribute('aria-labelledby');
		if (by) { const t = by.split(/\s+/).map(id => (document.getElementById(id) || {}).textContent || '').join(' ').trim(); if (t) return t; }
		const h = el.querySelector('h1,h2,h3,[role=heading]');
		return h ? h.textContent.trim() : '';
	};
	const candidates = [...document.querySelectorAll('[aria-modal="true"],dialog[open],[role="dialog"],[role="alertdialog"]')].filter(visible);
	const top = candidates.find(el => el.matches('[aria-modal="true"]') || (el.matches('dialog') && el.matches(':modal'))) || candidates[candidates.length - 1];
	const dialog = top ? (label(top).replace(/\s+/g, ' ').slice(0, 80) || 'untitled') : '';
	return { title: document.title, dialog };
})()`

func (a *Agent) readState(ctx context.Context, sess *browser.Session, sid string) *pageState {
	raw, err := dom.Eval(ctx, a.client(), sid, pageStateJS)
	if err != nil {
		return nil
	}
	var st pageState
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	st.tabs = map[string]bool{}
	for _, t := range sess.Tabs() {
		st.tabs[t.TargetID] = true
	}
	return &st
}

// changes lists what differs between two states, in the order an agent cares:
// a dialog (it now owns the screen), a new tab, the title.
func changes(before, after *pageState, tabs []browser.TabInfo) []string {
	if before == nil || after == nil {
		return nil
	}
	var out []string
	switch {
	case after.Dialog != "" && after.Dialog != before.Dialog:
		out = append(out, fmt.Sprintf("dialog opened: %q", after.Dialog))
	case after.Dialog == "" && before.Dialog != "":
		out = append(out, fmt.Sprintf("dialog closed: %q", before.Dialog))
	}
	for _, t := range tabs {
		if !before.tabs[t.TargetID] {
			line := fmt.Sprintf("new tab: [%d] %s", t.Index, t.URL)
			if t.Active {
				line += " (now the active tab)"
			}
			out = append(out, line)
		}
	}
	if after.Title != before.Title && strings.TrimSpace(after.Title) != "" {
		out = append(out, fmt.Sprintf("title: %q → %q", before.Title, after.Title))
	}
	return out
}
