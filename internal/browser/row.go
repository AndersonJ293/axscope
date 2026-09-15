// Linha de tabela achatada.
//
// Tabela é conteúdo, não ruído — mas o formato custava caro: uma linha de dados
// saía como cinco (a linha e as quatro células), e numa tabela de 60 linhas isso
// era 60% da leitura inteira. Aqui a linha vira uma só, desde que não se perca
// nada no caminho.
package browser

import "strings"

// separadorCelula separa as células de uma linha de tabela achatada.
const separadorCelula = " · "

// linhaDeRow tenta resumir uma linha de tabela numa linha só, devolvendo `false`
// quando não dá — e aí a leitura sai como sempre saiu, uma linha por célula.
//
// A conferência vem antes da coleta de propósito: juntar o texto marca os nós
// como consumidos, e isso não tem volta.
func (b *snapBuilder) linhaDeRow(nodeID string) (string, bool) {
	var celulas []string
	for _, cid := range b.children[nodeID] {
		c := b.nodes[cid]
		if c == nil || c.Ignored {
			continue
		}
		if !celulasPapel[c.Role.str()] {
			return "", false
		}
		if !b.podeAchatar(cid) {
			return "", false
		}
		celulas = append(celulas, cid)
	}
	if len(celulas) == 0 {
		return "", false
	}

	var vals []string
	for _, cid := range celulas {
		c := b.nodes[cid]
		t := norm(c.Name.str())
		if t == "" {
			// A célula já passou por podeAchatar — não tem alvo dentro —, então
			// a coleta não esbarra em rótulo de item.
			t, _ = b.textoDeContainer(cid)
		}
		// O separador não pode vir de dentro: viraria uma célula a mais para
		// quem lê.
		if strings.Contains(t, separadorCelula) {
			return "", false
		}
		vals = append(vals, t)
	}
	return strings.Join(vals, separadorCelula), true
}

// podeAchatar diz se o nó pode virar texto dentro de outra linha sem perder
// nada: sem alvo (senão a ref some), sem propriedade que a leitura mostra
// (`[checked]`, `[level=2]`…), e sem papel que carregue estrutura própria —
// imagem, lista e tabela aninhada continuam valendo linha.
func (b *snapBuilder) podeAchatar(nodeID string) bool {
	n := b.nodes[nodeID]
	if n == nil || n.Ignored || skipRoles[n.Role.str()] {
		return true
	}
	role := n.Role.str()
	if b.eligibleRef(n) || b.props(n) != "" {
		return false
	}
	if role != "StaticText" && role != "InlineTextBox" && !textuais[role] {
		return false
	}
	for _, c := range b.children[nodeID] {
		if !b.podeAchatar(c) {
			return false
		}
	}
	return true
}
