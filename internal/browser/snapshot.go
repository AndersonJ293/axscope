// Leitura da tela: a árvore de acessibilidade vira linhas legíveis, com `ref`
// estável para agir. Percorre, decide o que é ruído e o que é alvo.
package browser

import (
	"context"
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

	// Linha de tabela cujo conteúdo é só texto cabe numa linha só. Tabela é
	// conteúdo, não ruído: o que pesa é o formato — uma linha por célula custa
	// cinco linhas por linha de dados (medido: 305 das 508 linhas da leitura
	// numa tabela de 60 linhas). Com alvo dentro, a expansão fica, porque é ela
	// que carrega a ref.
	if role == "row" && name == "" && !b.refsOnly {
		if compacta, ok := b.linhaDeRow(nodeID); ok {
			b.emit(depth, "- row: "+compacta)
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
	} else if !b.refsOnly && !frameRoles[role] {
		// O iframe fica de fora da coleta: ele não tem texto próprio, e o que
		// ela pescaria é o texto do documento de dentro — que já aparece logo
		// abaixo, na árvore que foi enxertada nele.
		if texto, donos := b.textoDeContainer(nodeID); texto != "" && len(texto) <= textoDeResumo && !repeatOf(texto, parentName) {
			line += ": " + texto
			hadText = true
			for _, d := range donos {
				b.consumed[d] = true
			}
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
	b.walkFilhos(nodeID, name, depth+1, parentName)

	// Container que não rendeu linha nenhuma filha sai — mas só andaime, e só
	// quando ele mesmo não disse nada. Uma linha com nome diz conteúdo (é o
	// caso do rótulo "Candidato 413", cujo filho de texto é suprimido como eco
	// do nome do pai): apagá-la era apagar o rótulo inteiro.
	if ref == "" && name == "" && !hadText && len(b.out) == before {
		if scaffoldRoles[role] || landmarkRoles[role] {
			b.out = b.out[:len(b.out)-1]
		}
	}
}

// walkFilhos percorre os filhos em `depth`, resumindo irmãos idênticos (mesmo
// papel e nome) numa linha só.
//
// É método, e não um laço solto dentro do walk, para valer também no nível da
// raiz — antes o mapa de vistos só nascia ali dentro, e dois alvos idênticos
// filhos da raiz apareciam os dois.
//
// O primeiro irmão fica e os demais viram uma contagem na linha dele. Apagar em
// silêncio esconderia alvo: dois botões com o mesmo rótulo são nós diferentes,
// em posições diferentes. Assim o agente sabe que existe mais de um — e alcança
// o outro por css/pos.

func (b *snapBuilder) walkFilhos(nodeID, name string, depth int, parentName string) {
	childParent := parentName
	if name != "" {
		childParent = name
	}
	ids := b.childIDs(nodeID, name)

	chaves := make([]string, len(ids))
	contagem := map[string]int{}
	for i, cid := range ids {
		c := b.nodes[cid]
		if c == nil || c.Ignored {
			continue
		}
		cn := norm(c.Name.str())
		if cn == "" || !b.eligibleRef(c) {
			continue
		}
		k := c.Role.str() + "\x00" + cn
		chaves[i] = k
		contagem[k]++
	}

	vistos := map[string]bool{}
	for i, cid := range ids {
		k := chaves[i]
		if k != "" {
			if vistos[k] {
				continue
			}
			vistos[k] = true
		}
		marca := len(b.out)
		b.walk(cid, depth, childParent)
		// A linha do próprio filho é a primeira que ele emite, e a árvore é
		// lida de cima para baixo.
		if k != "" && contagem[k] > 1 && len(b.out) > marca {
			b.out[marca] += fmt.Sprintf(" (+%d iguais)", contagem[k]-1)
		}
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

func (b *snapBuilder) emit(depth int, line string) {
	if len(b.out) >= b.max {
		b.truncated = true
		return
	}
	b.out = append(b.out, strings.Repeat("  ", depth)+line)
}

// textoDeResumo é quanto texto a linha de um container pode resumir.
const textoDeResumo = 220

// textoDeContainer devolve o texto que a linha de um container pode resumir, com
// os nós que seriam consumidos — sem consumir nada ainda. Quem decide é quem
// chama, e só marca o que coube e foi mostrado.
//
// Duas guardas, e as duas vieram de medição no laboratório v2:
//
//   - Ramo com alvo dentro não entra. O texto ali é rótulo de um item — o
//     "Candidato 413" ao lado do botão "Abrir" —, e resumi-lo na linha do
//     container apaga justamente o que associa item e rótulo. Era assim que a
//     lista virtual virava catorze "Abrir" sem dono.
//   - O resumo é montado inteiro antes: texto que não cabe não é resumido, e
//     então ninguém é consumido. Antes o resumo era cortado em 220 caracteres
//     para exibir, mas consumia tudo o que tinha juntado — o resto sumia da
//     leitura sem aparecer em lugar nenhum.
func (b *snapBuilder) textoDeContainer(nodeID string) (string, []string) {
	var partes, donos []string
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
				partes = append(partes, t)
				donos = append(donos, cid)
			}
		case c.Ignored || (role == "generic" && norm(c.Name.str()) == ""):
			if b.temAlvo(cid) {
				continue
			}
			if inner, dentro := b.textoDeContainer(cid); inner != "" {
				partes = append(partes, inner)
				donos = append(donos, dentro...)
			}
		}
	}
	return strings.Join(partes, " "), donos
}

// temAlvo diz se a subárvore tem alvo acionável — ou seja, se o texto lá dentro
// é rótulo de um item, e não conteúdo solto que dá para resumir.
func (b *snapBuilder) temAlvo(nodeID string) bool {
	if v, ok := b.alvoCache[nodeID]; ok {
		return v
	}
	v := false
	if n := b.nodes[nodeID]; n != nil {
		v = !n.Ignored && b.eligibleRef(n)
		if !v {
			for _, c := range b.children[nodeID] {
				if b.temAlvo(c) {
					v = true
					break
				}
			}
		}
	}
	b.alvoCache[nodeID] = v
	return v
}
