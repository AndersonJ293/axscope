// Leitura da tela como texto: a árvore de acessibilidade vira linhas legíveis,
// com `ref` estável para agir. É o análogo do `tela` da ferramenta de QA do app.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ajunior/browser-use/internal/cdp"
)

// Snapshot é a tela lida, com o mapa de refs para o próximo passo.
type Snapshot struct {
	Text      string
	Refs      map[string]int // "e12" -> backendNodeId
	Count     int
	Title     string
	URL       string
	Truncated bool
}

// SnapshotOptions controla o tamanho da leitura.
type SnapshotOptions struct {
	MaxNodes int
	// RefsOnly lista só os alvos acionáveis, sem texto solto.
	RefsOnly bool
}

// interactiveRoles são os papéis que rendem `ref` (o agente pode agir neles).
var interactiveRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "searchbox": true,
	"checkbox": true, "radio": true, "combobox": true, "listbox": true,
	"option": true, "menuitem": true, "menuitemcheckbox": true,
	"menuitemradio": true, "tab": true, "switch": true, "slider": true,
	"spinbutton": true, "treeitem": true, "togglebutton": true,
	"menulist": true, "textfield": true, "popupbutton": true,
}

// skipRoles são papéis que nunca entram na leitura (ruído puro).
var skipRoles = map[string]bool{
	"ListMarker": true, "LineBreak": true, "none": true, "presentation": true,
	"InlineTextBox": true,
}

// layoutRoles são containers de layout: atravessa sem emitir linha, mesmo que
// tenham "nome" (é o caso dos LayoutTableCell de tabelas de layout).
var layoutRoles = map[string]bool{
	"LayoutTable": true, "LayoutTableRow": true, "LayoutTableCell": true,
	"LayoutTableColumn": true, "Row": true,
}

// structuralRoles são containers/papéis que ajudam a ler a página.
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

type axNode struct {
	NodeID   string `json:"nodeId"`
	Ignored  bool   `json:"ignored"`
	Role     axVal  `json:"role"`
	Name     axVal  `json:"name"`
	Value    axVal  `json:"value"`
	ParentID string `json:"parentId"`
	ChildIDs []string `json:"childIds"`
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

type snapBuilder struct {
	nodes     map[string]*axNode
	children  map[string][]string
	refs      map[string]int
	out       []string
	consumed  map[string]bool
	nextRef   int
	max       int
	refsOnly  bool
	truncated bool
}

// TakeSnapshot lê a tela da sessão (aba) informada.
func TakeSnapshot(ctx context.Context, client *cdp.Client, session string, opts SnapshotOptions) (*Snapshot, error) {
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 1500
	}
	var tree struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := client.SendJSON(ctx, "Accessibility.getFullAXTree", map[string]any{}, session, &tree); err != nil {
		return nil, err
	}
	if len(tree.Nodes) == 0 {
		return nil, fmt.Errorf("árvore de acessibilidade vazia")
	}

	b := &snapBuilder{
		nodes:    make(map[string]*axNode, len(tree.Nodes)),
		children: make(map[string][]string),
		refs:     make(map[string]int),
		consumed: make(map[string]bool),
		max:      opts.MaxNodes,
		refsOnly: opts.RefsOnly,
	}
	var root *axNode
	for i := range tree.Nodes {
		n := &tree.Nodes[i]
		b.nodes[n.NodeID] = n
		if n.ParentID == "" {
			root = n
		}
	}
	for _, n := range tree.Nodes {
		if n.ParentID != "" {
			b.children[n.ParentID] = append(b.children[n.ParentID], n.NodeID)
		}
	}
	if root == nil {
		root = &tree.Nodes[0]
	}

	for _, child := range b.children[root.NodeID] {
		b.walk(child, 0, false)
	}

	snap := &Snapshot{
		Text:      strings.Join(b.out, "\n"),
		Refs:      b.refs,
		Count:     len(b.out),
		Truncated: b.truncated,
	}
	snap.Title, _ = evalString(ctx, client, session, "document.title")
	snap.URL, _ = evalString(ctx, client, session, "location.href")
	return snap, nil
}

func (b *snapBuilder) walk(nodeID string, depth int, parentNamed bool) {
	n := b.nodes[nodeID]
	if n == nil || b.consumed[nodeID] {
		return
	}
	if n.Ignored {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentNamed)
		}
		return
	}

	role := n.Role.str()

	if skipRoles[role] {
		return
	}

	if role == "StaticText" || role == "InlineTextBox" {
		if !parentNamed && !b.refsOnly {
			text := norm(n.Name.str())
			if text == "" {
				text = norm(n.Value.str())
			}
			if text != "" {
				b.emit(depth, "- text: "+truncate(text, 220))
			}
		}
		return
	}

	// Containers de layout somem; os filhos herdam a profundidade.
	if layoutRoles[role] {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentNamed)
		}
		return
	}

	// Imagem dentro de um alvo já nomeado (link com imagem) é redundante.
	if (role == "img" || role == "image") && parentNamed {
		return
	}

	name := norm(n.Name.str())
	interesting := interactiveRoles[role] || structuralRoles[role] || name != ""

	if !interesting {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentNamed)
		}
		return
	}

	ref := b.refFor(n)
	if b.refsOnly && ref == "" {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentNamed)
		}
		return
	}

	line := "- "
	if role == "" || role == "generic" || role == "none" {
		line += "generic"
	} else {
		line += role
	}
	if name != "" {
		line += " " + strconv.Quote(name)
	} else if !b.refsOnly {
		if text := b.collectText(nodeID); text != "" {
			line += ": " + text
		}
	}
	if ref != "" {
		line += " [ref=" + ref + "]"
	}
	line += b.props(n)
	b.emit(depth, line)

	for _, c := range b.children[nodeID] {
		b.walk(c, depth+1, name != "")
	}
}

func (b *snapBuilder) emit(depth int, line string) {
	if len(b.out) >= b.max {
		b.truncated = true
		return
	}
	b.out = append(b.out, strings.Repeat("  ", depth)+line)
}

func (b *snapBuilder) refFor(n *axNode) string {
	if n.BackendDOMNodeID == 0 {
		return ""
	}
	role := n.Role.str()
	if !interactiveRoles[role] && !b.focusable(n) {
		return ""
	}
	b.nextRef++
	ref := "e" + strconv.Itoa(b.nextRef)
	b.refs[ref] = n.BackendDOMNodeID
	return ref
}

func (b *snapBuilder) focusable(n *axNode) bool {
	for _, p := range n.Properties {
		if p.Name == "focusable" {
			return rawBool(p.Value.Value)
		}
	}
	return false
}

// collectText junta o texto direto de um nó (atravessando só nós de texto),
// marcando-o como consumido para não repetir.
func (b *snapBuilder) collectText(nodeID string) string {
	var parts []string
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
				b.consumed[cid] = true
			}
		case c.Ignored || (role == "generic" && norm(c.Name.str()) == ""):
			if inner := b.collectText(cid); inner != "" {
				parts = append(parts, inner)
			}
		}
	}
	return truncate(strings.Join(parts, " "), 220)
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

// norm colapsa espaços/quebras e corta espaços das pontas.
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

func evalString(ctx context.Context, client *cdp.Client, session, expr string) (string, error) {
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	return res.Result.Value, nil
}
