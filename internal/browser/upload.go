// Upload de arquivo: os dois caminhos que a web usa para receber um.
//
// Um `<input type=file>` o navegador preenche em nome do usuário
// (`DOM.setFileInputFiles` dispara input/change como se o arquivo tivesse sido
// escolhido). É o caminho de formulário, e o input costuma estar escondido
// atrás de um botão — invisível na leitura, e ainda assim o alvo certo.
//
// Uma dropzone só entende o arquivo vindo de um arraste: aí o conteúdo vira um
// `File` dentro da página, dentro de um `DataTransfer` de verdade, e o drop é
// emitido sobre o alvo. `DataTransfer.files` é só-leitura por atribuição, mas
// `items.add(File)` popula — foi o que derrubou a suposição de que arquivo não
// se forja em JavaScript.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// PrimeiroInputDeArquivo devolve o primeiro `<input type=file>` da página.
//
// Existe porque, sem alvo, é ele que se quer: costuma estar escondido, e é o
// caminho que o navegador aceita sem diálogo nenhum.
func PrimeiroInputDeArquivo(ctx context.Context, client *cdp.Client, session string) (string, error) {
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "document.querySelector('input[type=file]')",
		"returnByValue": false,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.ObjectID == "" {
		return "", fmt.Errorf("não achei <input type=file> na página — passe alvo=<ref|css=|texto=>")
	}
	return res.Result.ObjectID, nil
}

// EhInputDeArquivo diz se o elemento é um `<input type=file>` (aceita o arquivo
// direto) ou qualquer outra coisa (só aceita por arraste).
func EhInputDeArquivo(ctx context.Context, client *cdp.Client, session, objectID string) (bool, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			return !!(this && this.tagName === 'INPUT' && this.type === 'file');
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return false, err
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return false, fmt.Errorf("não consegui ler o alvo")
	}
	return res.Result.Value, nil
}

// SetFileInput entrega `caminho` ao `<input type=file>`.
//
// O caminho é lido pelo navegador, não por nós: vale para quando os dois estão
// na mesma máquina (o caso da extensão). Fora disso, o caminho não existe do
// outro lado — aí o caminho é a dropzone, que viaja em bytes.
func SetFileInput(ctx context.Context, client *cdp.Client, session, objectID, caminho string) error {
	_, err := client.Send(ctx, "DOM.setFileInputFiles", map[string]any{
		"files":    []string{caminho},
		"objectId": objectID,
	}, session)
	return err
}

// SoltaArquivo emite um arraste de arquivo sobre o alvo, com o conteúdo de
// verdade dentro do DataTransfer.
func SoltaArquivo(ctx context.Context, client *cdp.Client, session string, t *Target, caminho string, p Presenter) error {
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return err
	}
	nome := filepath.Base(caminho)
	tipo := mime.TypeByExtension(filepath.Ext(nome))
	if tipo == "" {
		tipo = "application/octet-stream"
	}

	x, y := t.ondeAgir()
	if t.ObjectID != "" {
		_ = p.Spotlight(ctx, client, session, &t.Rect)
		_ = p.MoveCursor(ctx, client, session, x, y)
	}

	b64 := base64.StdEncoding.EncodeToString(dados)
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": soltaArquivoScript,
		"arguments": []any{
			map[string]any{"value": nome},
			map[string]any{"value": tipo},
			map[string]any{"value": b64},
			map[string]any{"value": x},
			map[string]any{"value": y},
		},
		"returnByValue": true,
	}, session)
	if err != nil {
		return err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		return fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	if res.Result.Value != "ok" {
		return fmt.Errorf("não consegui soltar o arquivo: %s", res.Result.Value)
	}
	return nil
}

// soltaArquivoScript monta o arquivo dentro da página e emite o arraste.
//
// A ordem importa para quem escuta: dragenter avisa que algo chegou, dragover
// costuma ser onde a dropzone se marca como alvo (e onde ela dá preventDefault
// para permitir o drop), e só então o drop entrega.
const soltaArquivoScript = `function (nome, tipo, b64, x, y) {
	const bin = atob(b64);
	const bytes = new Uint8Array(bin.length);
	for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
	const dt = new DataTransfer();
	dt.items.add(new File([bytes], nome, { type: tipo }));
	const sob = document.elementFromPoint(x, y) || this;
	if (!sob) return 'sem alvo sob o ponto';
	const dispara = (evento) => sob.dispatchEvent(new DragEvent(evento, {
		bubbles: true, cancelable: true, composed: true, dataTransfer: dt,
		clientX: x, clientY: y, screenX: x, screenY: y,
	}));
	dispara('dragenter');
	dispara('dragover');
	dispara('drop');
	dispara('dragend');
	return 'ok';
}`
