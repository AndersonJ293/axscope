package browser

import "testing"

// Regressão (missão 13): a leitura mostrava o iframe como uma linha só — o
// documento de dentro é outra árvore de acessibilidade, e sem juntar as duas o
// agente não ficava sabendo que existe um botão ali.
func TestEnxertarFrame_PoeOConteudoDentroDoIframe(t *testing.T) {
	pai := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("frame", "root", "Iframe", "", 42),
	}
	// A árvore do frame, como o CDP entrega: numerada a partir do próprio root e
	// com o nome do documento dela.
	filho := []axNode{
		ax("root", "", "RootWebArea", "Documento de dentro", 0),
		ax("h", "root", "heading", "Iframe zone", 0),
		ax("b", "root", "button", "Clique dentro", 43),
	}

	juntos := append(pai, enxertarFrame(filho, "f0:", "frame")...)
	snap := montarTexto(juntos, SnapshotOptions{})

	// A raiz do frame não vira linha: `RootWebArea "Documento de dentro"` seria
	// ruído dentro do iframe, e o que interessa é o conteúdo.
	esperado := `- Iframe
  - heading "Iframe zone"
  - button "Clique dentro" [ref=e1]`
	if snap.Text != esperado {
		t.Errorf("texto divergiu:\n--- obtido ---\n%s\n--- esperado ---\n%s", snap.Text, esperado)
	}
}

// O custo de ir buscar as árvores dos frames fica com quem tem iframe.
func TestTemIframe(t *testing.T) {
	if temIframe([]axNode{ax("a", "", "button", "x", 1)}) {
		t.Error("sem iframe na árvore, não devia ir buscar frames")
	}
	if !temIframe([]axNode{ax("a", "", "Iframe", "", 1)}) {
		t.Error("com iframe, tem de juntar")
	}
}
