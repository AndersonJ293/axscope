// Helpers de texto da leitura: normalizar, cortar e reconhecer o que não é
// conteúdo.
package browser

import (
	"strings"
	"unicode"
)

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

// --- resumo de texto de um container ---

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
