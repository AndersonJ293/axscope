// Leitura da tela como texto: a árvore de acessibilidade vira linhas legíveis,
// com `ref` estável para agir. É o análogo do `tela` da ferramenta de QA do app.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

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
	// Tudo desliga o corte de cromo de página (rodapé e links de atalho).
	Tudo bool
}

// noiseNameRe reconhece o "cromo de página": blocos de pular navegação, padrão
// WAI-ARIA presente em praticamente todo site e inútil para quem age por ref.
// Não é nome de site cravado — é o padrão de acessibilidade.
var noiseNameRe = regexp.MustCompile(`(?i)^(skip (to|navigation)|close jump menu)`)

// containerRoles são papéis que só agrupam: sem ref, sem texto próprio e sem
// filho visível, a linha não diz nada e sai.
var containerRoles = map[string]bool{
	"generic": true, "group": true, "figure": true, "list": true,
	"listitem": true, "region": true, "form": true, "toolbar": true,
	"navigation": true, "banner": true, "complementary": true, "main": true,
	"tablist": true, "menu": true, "menubar": true, "radiogroup": true,
}

// anonRoles são invólucros anônimos: sem nome, sem texto próprio e sem alvo,
// não valem linha — somem e os filhos sobem no lugar.
var anonRoles = map[string]bool{
	"": true, "none": true, "generic": true, "paragraph": true,
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

type snapBuilder struct {
	nodes     map[string]*axNode
	children  map[string][]string
	refs      map[string]int
	out       []string
	consumed  map[string]bool
	nextRef   int
	max       int
	refsOnly  bool
	tudo      bool
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
		tudo:     opts.Tudo,
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
		b.walk(child, 0, "")
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

	// Cromo de página: rodapé e blocos de pular navegação. Ninguém age neles.
	if !b.tudo {
		if role == "contentinfo" || noiseNameRe.MatchString(name) {
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

	// Containers de layout somem; os filhos herdam a profundidade.
	if layoutRoles[role] {
		for _, c := range b.children[nodeID] {
			b.walk(c, depth, parentName)
		}
		return
	}

	// Imagem dentro de um alvo já nomeado (link com imagem) é redundante.
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
	} else if !b.refsOnly {
		if text := b.collectText(nodeID); text != "" && !repeatOf(text, parentName) {
			line += ": " + text
			hadText = true
		}
	}

	// Invólucro anônimo (sem nome, sem texto próprio, sem alvo): não vira linha
	// — some, e os filhos sobem no lugar.
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

	// O nome do nó desce como contexto: filho que só o repete não é dito de novo.
	childParent := parentName
	if name != "" {
		childParent = name
	}

	seen := map[string]bool{}
	for _, cid := range b.childIDs(nodeID, name) {
		c := b.nodes[cid]
		// Irmãos idênticos (mesmo papel e nome) são o mesmo alvo oferecido duas
		// vezes; fica o primeiro.
		if c != nil && !c.Ignored {
			if cn := norm(c.Name.str()); cn != "" && b.eligibleRef(c) {
				key := c.Role.str() + "\x00" + cn
				if seen[key] {
					continue
				}
				seen[key] = true
			}
		}
		b.walk(cid, depth+1, childParent)
	}

	// Container que não rendeu nada (sem alvo, sem texto, sem filho) sai.
	if ref == "" && !hadText && containerRoles[role] && len(b.out) == before {
		b.out = b.out[:len(b.out)-1]
	}
}

// childIDs devolve os filhos a percorrer, pulando invólucros que só repetem o
// nome do pai — o clássico `link "X" > generic "X" > paragraph: X`. O alvo já
// está dito; os netos sobem para o lugar do invólucro.
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

// repeatOf diz se um texto é só eco do nome do ancestral (já dito acima).
func repeatOf(text, parent string) bool {
	if text == "" || parent == "" {
		return false
	}
	return strings.Contains(strings.ToLower(parent), strings.ToLower(text))
}

// separatorOnly diz se o texto é só pontuação de layout ("|", "·", "•") — não
// é conteúdo, é separador desenhado com texto.
func separatorOnly(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("|·•/–—»«›<>:;,.()[]{}\u00a0", r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func (b *snapBuilder) emit(depth int, line string) {
	if len(b.out) >= b.max {
		b.truncated = true
		return
	}
	b.out = append(b.out, strings.Repeat("  ", depth)+line)
}

// eligibleRef diz se o nó pode receber ref, sem consumir numeração.
func (b *snapBuilder) eligibleRef(n *axNode) bool {
	if n.BackendDOMNodeID == 0 {
		return false
	}
	role := n.Role.str()
	return interactiveRoles[role] || b.focusable(n)
}

func (b *snapBuilder) refFor(n *axNode) string {
	if !b.eligibleRef(n) {
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
