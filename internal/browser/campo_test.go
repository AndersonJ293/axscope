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
		{"select", campoDescritor{Tag: "SELECT"}, false, "bu select"},
		{"checkbox", campoDescritor{Tag: "INPUT", Tipo: "checkbox"}, false, "bu check"},
		{"radio", campoDescritor{Tag: "INPUT", Tipo: "radio"}, false, "bu check"},
		{"file", campoDescritor{Tag: "INPUT", Tipo: "file"}, false, "bu upload"},
		{"botão", campoDescritor{Tag: "INPUT", Tipo: "submit"}, false, "bu click"},
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
