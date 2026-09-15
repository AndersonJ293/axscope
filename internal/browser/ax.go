// The accessibility tree model, as the CDP delivers it, and the role tables that
// say what matters and what is page chrome.
package browser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var noiseNameRe = regexp.MustCompile(`(?i)^(skip (to|navigation)|close jump menu)`)

// scaffoldRoles are pure groupers: with no ref, no text of their own and no
// visible child, the line says nothing and drops out. That is the case of
// "Skip navigation menu".

var scaffoldRoles = map[string]bool{
	"generic": true, "group": true, "none": true, "": true,
}

// landmarkRoles are page landmarks. Empty, they are only worth it when they
// have a name: the name is the information. Without that, a
// `<div role="banner" aria-label="Top">` whose child only repeats "Top" vanished
// entirely — the fact that a banner exists there was lost.

var landmarkRoles = map[string]bool{
	"banner": true, "navigation": true, "main": true, "region": true,
	"complementary": true, "contentinfo": true, "form": true, "search": true,
}

// anonRoles are anonymous wrappers: with no name, no text of their own and no
// target, they are not worth a line — they vanish and the children rise in their
// place.

var anonRoles = map[string]bool{
	"": true, "none": true, "generic": true, "paragraph": true,
}

// interactiveRoles are the roles that yield a `ref` (the agent can act on them).

var interactiveRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "searchbox": true,
	"checkbox": true, "radio": true, "combobox": true, "listbox": true,
	"option": true, "menuitem": true, "menuitemcheckbox": true,
	"menuitemradio": true, "tab": true, "switch": true, "slider": true,
	"spinbutton": true, "treeitem": true, "togglebutton": true,
	"menulist": true, "textfield": true, "popupbutton": true,
}

// frameRoles are the roles of the element that hosts a separate document.
var frameRoles = map[string]bool{"Iframe": true, "iframe": true}

// skipRoles are roles that never enter the reading (pure noise).

var skipRoles = map[string]bool{
	"ListMarker": true, "LineBreak": true, "none": true, "presentation": true,
	"InlineTextBox": true,
}

// layoutRoles are layout containers: they pass through without emitting a line,
// even if they have a "name" (that is the case of the LayoutTableCell of layout
// tables).

var layoutRoles = map[string]bool{
	"LayoutTable": true, "LayoutTableRow": true, "LayoutTableCell": true,
	"LayoutTableColumn": true, "Row": true,
}

// structuralRoles are containers/roles that help read the page.

var structuralRoles = map[string]bool{
	"heading": true, "img": true, "image": true, "list": true, "listitem": true,
	"table": true, "row": true, "cell": true, "columnheader": true, "rowheader": true,
	"navigation": true, "main": true, "banner": true, "contentinfo": true,
	"complementary": true, "form": true, "dialog": true, "alertdialog": true,
	"alert": true, "search": true, "tabpanel": true, "tablist": true, "menu": true,
	"menubar": true, "group": true, "region": true, "article": true, "figure": true,
	"blockquote": true, "code": true, "term": true, "definition": true, "note": true,
	"status": true, "log": true, "tooltip": true, "progressbar": true,
	"separator": true, "iframe": true, "Iframe": true, "paragraph": true,
	"toolbar": true, "radiogroup": true,
}

// textualRoles are roles that vanish into a table row: they only carry text. A
// role not listed here keeps the row expanded — flattening is what can hide
// something, so the unknown is not flattened.
var textualRoles = map[string]bool{
	"": true, "none": true, "generic": true, "paragraph": true,
	"strong": true, "emphasis": true, "code": true, "term": true,
	"definition": true, "blockquote": true, "note": true,
	// The cell enters because it is the wrapper of the text inside the row:
	// without that the row could never be flattened — the check is done at the
	// cell.
	"cell": true, "gridcell": true, "columnheader": true, "rowheader": true,
}

// cellRoles are the roles the table row needs to have as children to fit in a
// single line.
var cellRoles = map[string]bool{
	"cell": true, "gridcell": true, "columnheader": true, "rowheader": true,
}

type axNode struct {
	NodeID     string   `json:"nodeId"`
	Ignored    bool     `json:"ignored"`
	Role       axVal    `json:"role"`
	Name       axVal    `json:"name"`
	Value      axVal    `json:"value"`
	ParentID   string   `json:"parentId"`
	ChildIDs   []string `json:"childIds"`
	Properties []struct {
		Name  string `json:"name"`
		Value struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"value"`
	} `json:"properties"`
	BackendDOMNodeID int `json:"backendDOMNodeId"`
}

type axVal struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

func (v axVal) str() string {
	if v.Value == nil {
		return ""
	}
	if s, ok := v.Value.(string); ok {
		return s
	}
	return fmt.Sprint(v.Value)
}

func (b *snapBuilder) props(n *axNode) string {
	var out strings.Builder
	level := ""
	for _, p := range n.Properties {
		switch p.Name {
		case "checked":
			if isMixed(p.Value.Value) {
				out.WriteString(" [checked=mixed]")
			} else if rawBool(p.Value.Value) {
				out.WriteString(" [checked]")
			}
		case "disabled":
			if rawBool(p.Value.Value) {
				out.WriteString(" [disabled]")
			}
		case "expanded":
			if rawBool(p.Value.Value) {
				out.WriteString(" [expanded]")
			} else {
				out.WriteString(" [collapsed]")
			}
		case "selected":
			if rawBool(p.Value.Value) {
				out.WriteString(" [selected]")
			}
		case "pressed":
			if isMixed(p.Value.Value) {
				out.WriteString(" [pressed=mixed]")
			} else if rawBool(p.Value.Value) {
				out.WriteString(" [pressed]")
			}
		case "focused":
			if rawBool(p.Value.Value) {
				out.WriteString(" [focused]")
			}
		case "required":
			if rawBool(p.Value.Value) {
				out.WriteString(" [required]")
			}
		case "readonly":
			if rawBool(p.Value.Value) {
				out.WriteString(" [readonly]")
			}
		case "level":
			level = strings.Trim(string(p.Value.Value), `"`)
		case "valuetext":
			if vt := norm(axRawString(p.Value.Value)); vt != "" {
				out.WriteString(" valuetext=" + strconv.Quote(truncate(vt, 80)))
			}
		}
	}
	if level != "" && level != "0" {
		out.WriteString(" [level=" + level + "]")
	}
	if v := norm(n.Value.str()); v != "" {
		role := n.Role.str()
		if role == "textbox" || role == "searchbox" || role == "combobox" ||
			role == "slider" || role == "spinbutton" {
			out.WriteString(" value=" + strconv.Quote(truncate(v, 80)))
		}
	}
	return out.String()
}

func rawBool(raw json.RawMessage) bool {
	s := string(raw)
	return s == "true" || s == `"true"`
}

func isMixed(raw json.RawMessage) bool {
	return strings.Contains(string(raw), "mixed")
}

func axRawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

// norm collapses spaces/newlines and trims leading/trailing spaces.
