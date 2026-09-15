package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ajunior/browser-use/internal/browser"
)

func TestRefGen(t *testing.T) {
	casos := []struct {
		spec string
		ref  string
		gen  int
		ok   bool
	}{
		{"e12#7", "e12", 7, true},
		{"e12", "e12", 0, false},
		{"#7", "#7", 0, false},
		{"e12#", "e12#", 0, false},
		{"e#a", "e#a", 0, false},
		// Seletor CSS com "#" não é ref com geração.
		{"css=#id", "css=#id", 0, false},
	}
	for _, c := range casos {
		ref, gen, ok := refGen(c.spec)
		if ref != c.ref || gen != c.gen || ok != c.ok {
			t.Errorf("refGen(%q) = (%q, %d, %v), esperado (%q, %d, %v)",
				c.spec, ref, gen, ok, c.ref, c.gen, c.ok)
		}
	}
}

func TestSplitTokens(t *testing.T) {
	casos := []struct {
		nome   string
		linha  string
		tokens []string
		erro   bool
	}{
		{"simples", "wait pronto", []string{"wait", "pronto"}, false},
		{"aspas duplas", `open "http://a b"`, []string{"open", "http://a b"}, false},
		{"aspas simples", "click 'e1'", []string{"click", "e1"}, false},
		{"string vazia entre aspas", `fill e1 ""`, []string{"fill", "e1", ""}, false},
		{"espaços múltiplos", "  wait   pronto  ", []string{"wait", "pronto"}, false},
		{"aspas não fechadas", `open "http://a`, nil, true},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := splitTokens(c.linha)
			if c.erro {
				if err == nil {
					t.Fatalf("esperava erro, veio %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if !reflect.DeepEqual(got, c.tokens) {
				t.Errorf("tokens = %#v, esperado %#v", got, c.tokens)
			}
		})
	}
}

// Regressão (laboratório v2): o clique que não chega respondia `ok` do mesmo
// jeito que um clique que funcionou — e ainda podia cair na camada de cima, com
// o efeito colateral que a página quisesse dar a ela. Agora ele é recusado, e a
// mensagem carrega o motivo e o próximo passo.
func TestFalhaDeAcao(t *testing.T) {
	err := falhaDeAcao("click", "text=Gostei 20", fmt.Errorf("o alvo está coberto por div.modal-backdrop — para clicar no ponto assim mesmo, use pos=x,y"))
	got := err.Error()
	for _, querido := range []string{"click em text=Gostei 20", "coberto por div.modal-backdrop", "pos=x,y"} {
		if !strings.Contains(got, querido) {
			t.Errorf("mensagem %q não diz %q", got, querido)
		}
	}
}

// Regressão: a leitura não dizia onde se está numa área que rola — a árvore de
// acessibilidade não carrega rolagem, e "rolar até o item 777 de 1000" virava
// chute ou conta de guardanapo. O cabeçalho da leitura agora diz.
func TestLinhaDeRolagem(t *testing.T) {
	if got := linhaDeRolagem(&browser.Snapshot{}); got != "" {
		t.Errorf("sem nada rolando o cabeçalho não devia mudar: %q", got)
	}

	snap := &browser.Snapshot{
		Pagina:        &browser.Rolagem{Alvo: "página", Pos: 2275, Max: 2830},
		Rolagens:      []browser.Rolagem{{Alvo: "#virtual", Pos: 32680, Max: 41672}},
		RolagensTotal: 2,
	}
	got := linhaDeRolagem(snap)
	for _, querido := range []string{"página 2275/2830", "#virtual 32680/41672"} {
		if !strings.Contains(got, querido) {
			t.Errorf("linha %q não diz %q", got, querido)
		}
	}

	// Muitas áreas: resume em vez de inventariar.
	muitas := &browser.Snapshot{RolagensTotal: 9}
	for i := 0; i < 8; i++ {
		muitas.Rolagens = append(muitas.Rolagens, browser.Rolagem{Alvo: "div", Pos: i, Max: 100})
	}
	if got := linhaDeRolagem(muitas); !strings.Contains(got, "(+4)") {
		t.Errorf("esperava o resumo das áreas que sobraram: %q", got)
	}
}

func TestSqueeze(t *testing.T) {
	casos := []struct{ entrada, querido string }{
		{"a\n\n\nb", "a\n\nb"},
		{"a  \nb\t\n", "a\nb"},
		{"\n\n  a  \n\n", "a"},
		{"a\nb\nc", "a\nb\nc"},
	}
	for _, c := range casos {
		if got := squeeze(c.entrada); got != c.querido {
			t.Errorf("squeeze(%q) = %q, esperado %q", c.entrada, got, c.querido)
		}
	}
}
