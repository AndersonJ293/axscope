package command

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ajunior/browser-use/internal/protocol"
)

func TestParse(t *testing.T) {
	casos := []struct {
		nome    string
		tokens  []string
		querido protocol.Request
		erro    bool
	}{
		{
			nome:    "posicional simples",
			tokens:  []string{"open", "https://x.test"},
			querido: protocol.Request{Cmd: "open", Args: map[string]any{"url": "https://x.test"}},
		},
		{
			nome:    "alias em português",
			tokens:  []string{"abrir", "https://x.test"},
			querido: protocol.Request{Cmd: "open", Args: map[string]any{"url": "https://x.test"}},
		},
		{
			nome:    "flag booleana",
			tokens:  []string{"open", "https://x.test", "--new"},
			querido: protocol.Request{Cmd: "open", Args: map[string]any{"url": "https://x.test", "new": true}},
		},
		{
			nome:    "chave=valor de argumento conhecido",
			tokens:  []string{"drag", "e1", "e2", "tipo=ponteiro"},
			querido: protocol.Request{Cmd: "drag", Args: map[string]any{"from": "e1", "to": "e2", "tipo": "ponteiro"}},
		},
		{
			// `k=v` só vira par quando `k` é conhecido; senão é posicional, e
			// um texto com "=" (JS, valor livre) não se perde como par.
			nome:    "texto com igual cai como posicional",
			tokens:  []string{"fill", "#a", "a=b"},
			querido: protocol.Request{Cmd: "fill", Args: map[string]any{"target": "#a", "value": "a=b"}},
		},
		{
			nome:    "posicional opcional omitido",
			tokens:  []string{"read"},
			querido: protocol.Request{Cmd: "read", Args: map[string]any{}},
		},
		{
			nome:    "posicional opcional preenchido",
			tokens:  []string{"read", "#conteudo"},
			querido: protocol.Request{Cmd: "read", Args: map[string]any{"selector": "#conteudo"}},
		},
		{
			nome:   "sem tokens",
			tokens: []string{},
			erro:   true,
		},
		{
			nome:   "comando desconhecido",
			tokens: []string{"voar"},
			erro:   true,
		},
		{
			nome:   "flag desconhecida",
			tokens: []string{"open", "https://x.test", "--voar"},
			erro:   true,
		},
		{
			nome:   "sobra argumento posicional",
			tokens: []string{"open", "a", "b"},
			erro:   true,
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := Parse(c.tokens)
			if c.erro {
				if err == nil {
					t.Fatalf("esperava erro, veio %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if !reflect.DeepEqual(got, c.querido) {
				t.Errorf("pedido divergiu:\n obtido: %+v\nquerido: %+v", got, c.querido)
			}
		})
	}
}

// Regressão: `fill` e `type` não conseguiam mirar por `text=`. O token era
// engolido como par chave=valor, porque o posicional se chamava `text` — logo,
// justamente nos dois comandos em que mais se quer mirar por rótulo.
func TestParse_FillMiraPorTexto(t *testing.T) {
	for _, cmd := range []string{"fill", "type"} {
		req, err := Parse([]string{cmd, "text=CAPTCHA", "A7K9P"})
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		if req.Args["target"] != "text=CAPTCHA" || req.Args["value"] != "A7K9P" {
			t.Errorf("%s: args = %v", cmd, req.Args)
		}
	}
}

// A ajuda deriva da tabela: todo spec aparece na lista.
func TestHelpListaTodosOsSpecs(t *testing.T) {
	help := Help()
	for _, spec := range Specs {
		if !strings.Contains(help, spec.Cmd) {
			t.Errorf("ajuda não menciona %q", spec.Cmd)
		}
	}
}

// Regressão: o handler do wait lê `timeout`, mas o comando não expunha a opção
// — Parse recusava como sobra, e a opção era inalcançável.
func TestParse_WaitComTimeout(t *testing.T) {
	req, err := Parse([]string{"wait", "carregando", "5000"})
	if err != nil {
		t.Fatalf("Parse recusou o timeout: %v", err)
	}
	if req.Args["text"] != "carregando" || req.Args["timeout"] != "5000" {
		t.Errorf("args = %v", req.Args)
	}
	// E sem o timeout continua valendo: ele é opcional.
	if _, err := Parse([]string{"wait", "carregando"}); err != nil {
		t.Errorf("wait sem timeout deveria valer: %v", err)
	}
}
