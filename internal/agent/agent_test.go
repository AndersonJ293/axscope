package agent

import (
	"reflect"
	"testing"
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
