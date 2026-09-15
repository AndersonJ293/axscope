package protocol

import "testing"

// Regressão: valor que chega da CLI ou do MCP é texto, não número. Sem o caso
// `string`, toda opção numérica (timeout do wait) era inerte: Request.Int
// devolvia o padrão e ninguém percebia.
func TestInt(t *testing.T) {
	casos := []struct {
		nome  string
		args  map[string]any
		def   int
		quero int
	}{
		{"string da CLI", map[string]any{"timeout": "5000"}, 0, 5000},
		{"string com espaço", map[string]any{"timeout": " 700 "}, 0, 700},
		{"string inválida cai no padrão", map[string]any{"timeout": "abc"}, 42, 42},
		{"float do JSON", map[string]any{"timeout": float64(1500)}, 0, 1500},
		{"int direto", map[string]any{"timeout": 3}, 0, 3},
		{"ausente", map[string]any{}, 9, 9},
		{"args nulo", nil, 9, 9},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			r := Request{Cmd: "wait", Args: c.args}
			if v := r.Int("timeout", c.def); v != c.quero {
				t.Errorf("Int = %d, esperado %d", v, c.quero)
			}
		})
	}
}
