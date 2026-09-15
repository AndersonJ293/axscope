// O modelo da árvore de acessibilidade, como o CDP entrega, e as tabelas de
// papel que dizem o que interessa e o que é cromo de página.
package browser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var noiseNameRe = regexp.MustCompile(`(?i)^(skip (to|navigation)|close jump menu)`)

// scaffoldRoles são agrupadores puros: sem ref, sem texto próprio e sem filho
// visível, a linha não diz nada e sai. É o caso do "Skip navigation menu".

var scaffoldRoles = map[string]bool{
	"generic": true, "group": true, "none": true, "": true,
}

// landmarkRoles são marcos da página. Vazio, só valem se tiverem nome: o nome é
// a informação. Sem isso, um `<div role="banner" aria-label="Topo">` cujo filho
// só repete "Topo" sumia inteiro — perdia-se que existe um banner ali.

var landmarkRoles = map[string]bool{
	"banner": true, "navigation": true, "main": true, "region": true,
	"complementary": true, "contentinfo": true, "form": true, "search": true,
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

// textuais são papéis que somem para dentro de uma linha de tabela: só carregam
// texto. Papel que não está aqui mantém a linha expandida — achatar é o que pode
// esconder coisa, então o desconhecido não é achatado.
var textuais = map[string]bool{
	"": true, "none": true, "generic": true, "paragraph": true,
	"strong": true, "emphasis": true, "code": true, "term": true,
	"definition": true, "blockquote": true, "note": true,
	// A célula entra porque é ela o invólucro do texto dentro da linha: sem
	// isso a linha nunca poderia ser achatada — a conferência é feita na célula.
	"cell": true, "gridcell": true, "columnheader": true, "rowheader": true,
}

// celulasPapel são os papéis que a linha de tabela precisa ter como filhos para
// caber numa linha só.
var celulasPapel = map[string]bool{
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

// norm colapsa espaços/quebras e corta espaços das pontas.
