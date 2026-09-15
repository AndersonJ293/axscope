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

// Regressão: o `scroll` não tinha como mirar um container. A lista de infinite
// scroll do laboratório mostrou a falta: rolar a página não carrega o próximo
// lote, e o gesto sem alvo rolava o que estivesse sob o centro da tela.
func TestParse_ScrollComAlvo(t *testing.T) {
	req, err := Parse([]string{"scroll", "200", "alvo=text=Infinite item 3"})
	if err != nil {
		t.Fatalf("Parse recusou o alvo: %v", err)
	}
	if req.Args["dy"] != "200" || req.Args["alvo"] != "text=Infinite item 3" {
		t.Errorf("args = %v", req.Args)
	}
	// E sem alvo continua valendo: ele é opcional.
	req, err = Parse([]string{"scroll", "500"})
	if err != nil {
		t.Fatalf("scroll sem alvo deveria valer: %v", err)
	}
	if req.Args["dy"] != "500" {
		t.Errorf("args = %v", req.Args)
	}
}

// Regressão: o `upload` declara o posicional como `alvo`, e o handler lê
// `alvo`. O nome tem que bater: quando não batia, o alvo caía no vazio e o
// comando escolhia sempre o primeiro `<input type=file>` — e o laboratório
// aceitou por acidente, porque a dropzone dele tem um input escondido dentro.
func TestParse_UploadComAlvo(t *testing.T) {
	req, err := Parse([]string{"upload", "/tmp/a.txt"})
	if err != nil {
		t.Fatalf("Parse recusou o upload: %v", err)
	}
	if req.Args["arquivo"] != "/tmp/a.txt" {
		t.Errorf("sem alvo: args = %v", req.Args)
	}

	req, err = Parse([]string{"upload", "/tmp/a.txt", "alvo=css=#dropzone"})
	if err != nil {
		t.Fatalf("Parse recusou o alvo: %v", err)
	}
	if req.Args["arquivo"] != "/tmp/a.txt" || req.Args["alvo"] != "css=#dropzone" {
		t.Errorf("com alvo: args = %v", req.Args)
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
	req, err = Parse([]string{"wait", "carregando"})
	if err != nil {
		t.Errorf("wait sem timeout deveria valer: %v", err)
	}
}

// Regressão (laboratório v3): `wait` por texto casava em qualquer lugar da
// página — a espera por um cargo que também aparecia na barra lateral voltou em
// 2ms, com o dropdown da busca ainda fechado. `dentro=` limita a busca; e as
// flags de estado esperam por um alvo que só habilita depois.
func TestParse_WaitComEscopoEEstado(t *testing.T) {
	req, err := Parse([]string{"wait", "Senior Recruiter", "5000", "dentro=css=#suggest"})
	if err != nil {
		t.Fatalf("Parse recusou o escopo: %v", err)
	}
	if req.Args["text"] != "Senior Recruiter" || req.Args["timeout"] != "5000" || req.Args["dentro"] != "css=#suggest" {
		t.Errorf("args = %v", req.Args)
	}

	req, err = Parse([]string{"wait", "css=#submitApply", "8000", "--habilitado"})
	if err != nil {
		t.Fatalf("Parse recusou o estado: %v", err)
	}
	if req.Args["habilitado"] != true || req.Args["text"] != "css=#submitApply" {
		t.Errorf("args = %v", req.Args)
	}

	// waitgone também aceita escopo.
	if req, err := Parse([]string{"waitgone", "Carregando", "dentro=css=#lista"}); err != nil {
		t.Fatalf("waitgone com escopo: %v", err)
	} else if req.Args["dentro"] != "css=#lista" {
		t.Errorf("args = %v", req.Args)
	}
}
