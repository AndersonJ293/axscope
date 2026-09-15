// Atribuição de ref: quem pode receber uma, e com que número.
package browser

import "strconv"

// eligibleRef diz se o nó pode receber ref, sem consumir numeração.
func (b *snapBuilder) eligibleRef(n *axNode) bool {
	if n.BackendDOMNodeID == 0 {
		return false
	}
	role := n.Role.str()
	return interactiveRoles[role] || b.focusable(n)
}

// refFor dá a próxima ref ao nó, com a geração da leitura no nome. A geração é o
// que faz uma ref de leitura antiga ser recusada em vez de apontar em silêncio
// para outro elemento.
func (b *snapBuilder) refFor(n *axNode) string {
	if !b.eligibleRef(n) {
		return ""
	}
	b.nextRef++
	ref := "e" + strconv.Itoa(b.nextRef)
	if b.gen > 0 {
		ref += "#" + strconv.Itoa(b.gen)
	}
	b.refs[ref] = n.BackendDOMNodeID
	return ref
}

// focusable pergunta à árvore se o nó recebe foco — é o que faz um elemento
// focável sem papel interativo (tabindex, contenteditable) virar alvo.
func (b *snapBuilder) focusable(n *axNode) bool {
	for _, p := range n.Properties {
		if p.Name == "focusable" {
			return rawBool(p.Value.Value)
		}
	}
	return false
}
