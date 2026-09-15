package browser

import (
	"strings"
	"testing"
)

// Regressão (missão 12 do laboratório): a árvore de acessibilidade **achata**
// shadow DOM, então a leitura mostra o botão que está dentro de um shadow root
// — com nome e ref, e o ref até alcança. A mira por DOM andava só no documento
// claro: a leitura mostrava e o `text=` não encontrava.
func TestExpressoesDeAlvoAtravessamShadowRoot(t *testing.T) {
	if expr := expressaoTexto("Botão no Shadow DOM"); !strings.Contains(expr, "shadowRoot") {
		t.Error("expressaoTexto não atravessa shadow root — a leitura mostra o que está dentro e a mira não alcança")
	}

	// No css= a ordem importa: o documento claro primeiro, porque é a semântica
	// do seletor; a sombra é saída, não preferência. Assim uma página que sempre
	// funcionou não muda de alvo.
	expr := expressaoCSS("#shadowButton")
	if !strings.Contains(expr, "document.querySelector") {
		t.Error("expressaoCSS não tenta o documento claro")
	}
	if !strings.Contains(expr, "|| sobASombra(") {
		t.Error("expressaoCSS não cai na sombra quando o claro não acha")
	}
}
