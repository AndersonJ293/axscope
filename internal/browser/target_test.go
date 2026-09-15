package browser

import (
	"strings"
	"testing"

	"github.com/ajunior/browser-use/internal/dom"
)

// Regressão (missão 13 do laboratório): `pos=x,y` resolvia o elemento sob o
// ponto e depois agia no **centro dele**. Sobre um iframe isso é o centro do
// iframe — a dezenas de pixels do botão pedido — e o clique acertava o vazio.
func TestAlvoPorPosicaoAgeNoPontoPedido(t *testing.T) {
	alvo := &Target{
		Rect:  dom.Rect{X: 279, Y: 428, Width: 628, Height: 170},
		Ponto: &Ponto{X: 385, Y: 527},
	}
	x, y := alvo.ondeAgir()
	if x != 385 || y != 527 {
		t.Errorf("agiu em %v,%v — devia agir no ponto pedido, não no centro do elemento (%v,%v)",
			x, y, alvo.Rect.X+alvo.Rect.Width/2, alvo.Rect.Y+alvo.Rect.Height/2)
	}

	// Sem posição, o centro do elemento continua valendo.
	so := &Target{Rect: dom.Rect{X: 0, Y: 0, Width: 100, Height: 50}}
	if x, y := so.ondeAgir(); x != 50 || y != 25 {
		t.Errorf("sem pos= o centro virou %v,%v", x, y)
	}
}

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
