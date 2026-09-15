// O passeio pela árvore: o que vira linha, o que some e o que se resume.
package browser

import (
	"fmt"
	"strconv"
	"strings"
)

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
