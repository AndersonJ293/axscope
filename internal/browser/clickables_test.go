package browser

import (
	"strings"
	"testing"
)

// A seção precisa dar o seletor e o rótulo, resumir o que não coube, e sumir
// quando não há nada — a leitura não pode ganhar um bloco vazio.
func TestSecaoDeClicaveis(t *testing.T) {
	if got := secaoDeClicaveis(nil, 0); got != "" {
		t.Errorf("sem itens a seção não devia existir: %q", got)
	}

	itens := []Clickable{
		{Selector: `div.thread[data-id="t1"]`, Label: "Ana Souza — Oi Ana! Tenho interesse"},
		{Selector: `div.thread[data-id="t2"]`, Label: "Bruno Lima — Aquele artigo sobre filas"},
	}
	got := secaoDeClicaveis(itens, 2)
	for _, querido := range []string{"clicáveis", `div.thread[data-id="t1"]`, "Ana Souza", `div.thread[data-id="t2"]`} {
		if !strings.Contains(got, querido) {
			t.Errorf("seção %q não diz %q", got, querido)
		}
	}
	if strings.Contains(got, "(+") {
		t.Errorf("nada ficou de fora, não devia resumir: %q", got)
	}

	// Acima do teto, o resto vira contagem.
	muitos := make([]Clickable, 0, maxClicaveis+4)
	for i := 0; i < maxClicaveis+4; i++ {
		muitos = append(muitos, Clickable{Selector: "div.item", Label: "item"})
	}
	got = secaoDeClicaveis(muitos, len(muitos))
	if !strings.Contains(got, "(+4)") {
		t.Errorf("esperava o resumo do que sobrou: %q", got)
	}
	if n := strings.Count(got, "\n  div.item"); n != maxClicaveis {
		t.Errorf("mostrou %d itens, esperado %d", n, maxClicaveis)
	}
}
