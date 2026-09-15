package mcpsrv

import "testing"

// Regressão (laboratório v3): as mensagens de recusa do `fill` apontam para
// `axscope select`, `axscope check` e `axscope upload` — e nenhum dos três estava no catálogo
// exposto. O agente foi mandado usar o que não podia chamar, e teve de recorrer
// ao `eval` para escolher uma opção de `<select>`.
//
// O teste prende o contrato: o comando que a ferramenta manda usar precisa estar
// alcançável.
func TestCatalogoCobreOsComandosQueAsMensagensCitam(t *testing.T) {
	exigidos := []string{"select", "check", "uncheck", "type", "upload"}
	expostos := map[string]bool{}
	for _, td := range tools() {
		expostos[td.Name] = true
	}
	for _, cmd := range exigidos {
		if !expostos[cmd] {
			t.Errorf("%q não está exposto no catálogo, mas as mensagens de recusa mandam usá-lo", cmd)
		}
	}
}

// O catálogo continua sendo um conjunto enxuto: se crescer por descuido, é aqui
// que se percebe (cada schema custa contexto em toda requisição).
func TestCatalogoNaoCrescePorDescuido(t *testing.T) {
	if n := len(tools()); n > 30 {
		t.Errorf("o catálogo curado tem %d ferramentas — acima disso o custo de contexto deixa de compensar", n)
	}
}
