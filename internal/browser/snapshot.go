// Leitura da tela: a árvore de acessibilidade vira linhas legíveis, com `ref`
// estável para agir. Percorre, decide o que é ruído e o que é alvo.
package browser

import (
	"context"
	"fmt"
	"strings"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Snapshot é a tela lida, com o mapa de refs para o próximo passo.
type Snapshot struct {
	Text      string
	Refs      map[string]int // "e12" -> backendNodeId
	Count     int
	Title     string
	URL       string
	Truncated bool
	// Pagina é o estado de rolagem do documento, e Rolagens são as áreas que
	// rolam dentro dele. Vêm do DOM: a árvore de acessibilidade não carrega
	// rolagem.
	Pagina        *Rolagem
	Rolagens      []Rolagem
	RolagensTotal int
}

// SnapshotOptions controla o tamanho da leitura.

type SnapshotOptions struct {
	MaxNodes int
	// RefsOnly lista só os alvos acionáveis, sem texto solto.
	RefsOnly bool
	// Tudo desliga o corte de cromo de página (rodapé e links de atalho).
	Tudo bool
	// Gen é a geração da leitura. Entra na ref (e12#7) para que uma ref de
	// leitura antiga seja recusada em vez de apontar para outro elemento.
	Gen int
}

// noiseNameRe reconhece o "cromo de página": blocos de pular navegação, padrão
// WAI-ARIA presente em praticamente todo site e inútil para quem age por ref.
// Não é nome de site cravado — é o padrão de acessibilidade.

type snapBuilder struct {
	nodes     map[string]*axNode
	children  map[string][]string
	refs      map[string]int
	out       []string
	consumed  map[string]bool
	alvoCache map[string]bool
	nextRef   int
	max       int
	refsOnly  bool
	tudo      bool
	gen       int
	truncated bool
}

// TakeSnapshot lê a tela da sessão (aba) informada.

func TakeSnapshot(ctx context.Context, client *cdp.Client, session string, opts SnapshotOptions) (*Snapshot, error) {
	var tree struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := client.SendJSON(ctx, "Accessibility.getFullAXTree", map[string]any{}, session, &tree); err != nil {
		return nil, err
	}
	if len(tree.Nodes) == 0 {
		return nil, fmt.Errorf("árvore de acessibilidade vazia")
	}

	// Iframe é outra árvore: a do frame principal mostra o iframe como uma linha
	// só. Sem juntar, a leitura não diz o que tem dentro.
	nodes := tree.Nodes
	if temIframe(nodes) {
		nodes = juntarFrames(ctx, client, session, nodes)
	}

	snap := montarTexto(nodes, opts)
	meta := lerMetaDaPagina(ctx, client, session)
	snap.Title, snap.URL = meta.Title, meta.URL
	snap.Pagina, snap.Rolagens, snap.RolagensTotal = meta.Pagina, meta.Rolagens, meta.Total

	// Alvos que a árvore não marca (div com handler de clique) entram como uma
	// seção no fim: sem eles, o agente tem de adivinhar seletor para metade dos
	// botões de um app real.
	clicaveis, total := lerClicaveis(ctx, client, session)
	if secao := secaoDeClicaveis(clicaveis, total); secao != "" {
		snap.Text += "\n" + secao
		snap.Count += strings.Count(secao, "\n") + 1
	}
	return snap, nil
}

// montarTexto é a parte pura da leitura: transforma a árvore de acessibilidade
// crua em linhas e refs, sem tocar CDP. Fica separada de TakeSnapshot porque é
// onde mora todo o corte de ruído — a lógica mais frágil do projeto — e é o que
// dá para testar com fixture.

func montarTexto(nodes []axNode, opts SnapshotOptions) *Snapshot {
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 1500
	}
	b := &snapBuilder{
		nodes:     make(map[string]*axNode, len(nodes)),
		children:  make(map[string][]string),
		refs:      make(map[string]int),
		consumed:  make(map[string]bool),
		alvoCache: make(map[string]bool),
		max:       opts.MaxNodes,
		refsOnly:  opts.RefsOnly,
		tudo:      opts.Tudo,
		gen:       opts.Gen,
	}
	var root *axNode
	for i := range nodes {
		n := &nodes[i]
		b.nodes[n.NodeID] = n
		if n.ParentID == "" {
			root = n
		}
	}
	for _, n := range nodes {
		if n.ParentID != "" {
			b.children[n.ParentID] = append(b.children[n.ParentID], n.NodeID)
		}
	}
	if root == nil {
		root = &nodes[0]
	}

	// A raiz não é uma linha: os filhos dela começam na profundidade 0, e passam
	// pelo mesmo corte de irmãos idênticos do resto da árvore.
	b.walkFilhos(root.NodeID, "", 0, "")

	return &Snapshot{
		Text:      strings.Join(b.out, "\n"),
		Refs:      b.refs,
		Count:     len(b.out),
		Truncated: b.truncated,
	}
}
