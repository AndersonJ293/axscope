package browser

import (
	"strings"
	"testing"
)

// Regressão (laboratório v2): `fill` num `<select>` respondia ok e o valor
// continuava o de antes; `fill` num `<label>` respondia ok sem ter onde
// escrever. A recusa precisa dizer o comando que faz o que se queria.
func TestClassificaCampo(t *testing.T) {
	casos := []struct {
		nome     string
		campo    campoDescritor
		aceita   bool
		contemNa string
	}{
		{"input de texto", campoDescritor{Tag: "INPUT", Tipo: "text"}, true, ""},
		{"input sem type", campoDescritor{Tag: "INPUT"}, true, ""},
		{"input number", campoDescritor{Tag: "INPUT", Tipo: "number"}, true, ""},
		{"textarea", campoDescritor{Tag: "TEXTAREA"}, true, ""},
		{"contenteditable", campoDescritor{Tag: "DIV", Editavel: true}, true, ""},
		{"role textbox", campoDescritor{Tag: "DIV", Papel: "textbox"}, true, ""},
		{"select", campoDescritor{Tag: "SELECT"}, false, "axscope select"},
		{"checkbox", campoDescritor{Tag: "INPUT", Tipo: "checkbox"}, false, "axscope check"},
		{"radio", campoDescritor{Tag: "INPUT", Tipo: "radio"}, false, "axscope check"},
		{"file", campoDescritor{Tag: "INPUT", Tipo: "file"}, false, "axscope upload"},
		{"botão", campoDescritor{Tag: "INPUT", Tipo: "submit"}, false, "axscope click"},
		{"label", campoDescritor{Tag: "LABEL"}, false, "não é campo de texto"},
		{"div", campoDescritor{Tag: "DIV"}, false, "não é campo de texto"},
	}
	for _, c := range casos {
		aceita, motivo := classificaCampo(c.campo)
		if aceita != c.aceita {
			t.Errorf("%s: aceita = %v, esperado %v (motivo %q)", c.nome, aceita, c.aceita, motivo)
		}
		if !aceita && !strings.Contains(motivo, c.contemNa) {
			t.Errorf("%s: motivo %q não menciona %q", c.nome, motivo, c.contemNa)
		}
	}
}

// Regressão (laboratório v3): `press Enter` disparava o handler e não quebrava a
// linha — o CDP só insere com `text` no keyDown, e o Enter ficava sem. "primeira"
// + Enter + "segunda" virava "primeirasegunda".
func TestTextoDaTecla(t *testing.T) {
	if got := textoDaTecla("Enter"); got != "\r" {
		t.Errorf("Enter tem de inserir a quebra, e insere %q", got)
	}
	if got := textoDaTecla("Tab"); got != "\t" {
		t.Errorf("Tab tem de inserir a tabulação, e insere %q", got)
	}
	for _, tecla := range []string{"Escape", "Backspace", "ArrowDown", "a"} {
		if got := textoDaTecla(tecla); got != "" {
			t.Errorf("%q não insere nada, mas insere %q", tecla, got)
		}
	}
}

// Regressão (laboratório v3): digitar uma quebra de linha não inseria nada — o
// `type` mandava o caractere como texto, e texto com `\n` não entra no campo.
// "alfa\nbeta" virava "alfabeta", em silêncio.
func TestTeclaParaEQuebras(t *testing.T) {
	for _, r := range []rune{'\n', '\r'} {
		if tecla := teclaPara(r); tecla != "Enter" {
			t.Errorf("quebra de linha (%q) tem de virar Enter, virou %q", r, tecla)
		}
	}
	for _, r := range []rune{'a', 'ç', ' ', '\t'} {
		if tecla := teclaPara(r); tecla != "" {
			t.Errorf("%q não é tecla nomeada, virou %q", r, tecla)
		}
	}

	if got := normalizarQuebras("a\r\nb\rc"); got != "a\nb\rc" {
		t.Errorf("normalizarQuebras = %q", got)
	}
}

// Máscara muda o valor de propósito (o telefone vira "(77) 9 9999-1111"), então
// diferença não é sinal de nada. Continuar vazio é.
func TestAvisoDePreenchimento(t *testing.T) {
	if got := avisoDePreenchimento("(77) 99999-1111", "(77) 9 9999-1111"); got != "" {
		t.Errorf("máscara não é falha: %q", got)
	}
	if got := avisoDePreenchimento("texto", "   "); got == "" {
		t.Error("campo vazio depois de preencher precisa avisar")
	}
	if got := avisoDePreenchimento("", ""); got != "" {
		t.Errorf("sem texto pedido não há o que avisar: %q", got)
	}
}
