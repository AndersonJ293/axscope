// Campo de texto: quem aceita o que se quer escrever, e o que conferir depois.
//
// Medido no laboratório v2: `fill` num `<select>` respondia ok e o valor
// continuava o de antes; `fill` num `<label>` respondia ok sem ter onde
// escrever. Texto que não entra é pior do que erro — o agente segue como se
// tivesse preenchido, e só descobre o contrário ao conferir o resultado. Por
// isso a recusa diz o comando que faz o que se queria.
package browser

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// campoDescritor é o que a página diz do alvo antes de receber texto.
type campoDescritor struct {
	Tag      string `json:"tag"`
	Tipo     string `json:"tipo"`
	Editavel bool   `json:"editavel"`
	Papel    string `json:"papel"`
}

// descreveCampo pergunta à página o que é o alvo. Falha em silêncio: sem
// descrição, o campo é tratado como aceito — quem barra o errado é o aviso do
// fim, não um palpite nosso.
func descreveCampo(ctx context.Context, client *cdp.Client, session, objectID string) campoDescritor {
	var d campoDescritor
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			return {
				tag: this.tagName || '',
				tipo: (this.getAttribute && this.getAttribute('type')) || '',
				editavel: !!this.isContentEditable,
				papel: (this.getAttribute && this.getAttribute('role')) || ''
			};
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return d
	}
	var res struct {
		Result struct {
			Value campoDescritor `json:"value"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &res)
	return res.Result.Value
}

// classificaCampo diz se o alvo aceita texto digitado — e, quando não aceita, o
// comando que faz o que se queria.
func classificaCampo(d campoDescritor) (bool, string) {
	tag := strings.ToLower(d.Tag)
	tipo := strings.ToLower(d.Tipo)
	switch {
	case tag == "textarea" || d.Editavel:
		return true, ""
	case tag == "select":
		return false, "é um <select> — para escolher a opção use `axscope select <alvo> <valor>`"
	case tag == "input":
		switch tipo {
		case "checkbox", "radio":
			return false, "é uma caixa de marcar — use `axscope check <alvo>` (ou `uncheck`)"
		case "file":
			return false, "recebe arquivo — use `axscope upload <arquivo> alvo=<alvo>`"
		case "submit", "button", "reset", "image":
			return false, "é um botão — use `axscope click <alvo>`"
		}
		return true, ""
	case d.Papel == "textbox" || d.Papel == "searchbox":
		return true, ""
	default:
		return false, "não é campo de texto (é <" + tag + ">) — texto vai em input, textarea ou contenteditable"
	}
}

// avisoDePreenchimento diz quando o texto não entrou.
//
// Campo com máscara muda o valor de propósito (o telefone vira "(77) 9 9999…"),
// então diferença não é sinal de nada. Continuar vazio é: nada entrou.
func avisoDePreenchimento(enviado, valor string) string {
	if enviado != "" && strings.TrimSpace(valor) == "" {
		return "o campo continua vazio — o texto não entrou"
	}
	return ""
}

// normalizarQuebras troca CRLF por LF: o texto vem de onde vier (Windows, um
// arquivo, o agente), e o campo não tem o que fazer com o `\r` sobrando.
func normalizarQuebras(texto string) string {
	return strings.ReplaceAll(texto, "\r\n", "\n")
}

// teclaPara devolve a tecla nomeada que representa o caractere — ou "", quando o
// caractere vai como texto mesmo.
//
// É só a quebra de linha, e ela importa: mandar `\n` como texto não insere nada,
// então o caractere sumia em silêncio na digitação caractere a caractere —
// medido no laboratório v3, `alfa\nbeta` virava `alfabeta`.
func teclaPara(r rune) string {
	if r == '\n' || r == '\r' {
		return "Enter"
	}
	return ""
}

// valorDoCampo lê o que o campo tem depois do preenchimento.
func valorDoCampo(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (this.isContentEditable) return this.innerText || '';
			if ('value' in this) return String(this.value || '');
			return this.innerText || '';
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return ""
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.Result.Value
}
