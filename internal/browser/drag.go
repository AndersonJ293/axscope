// Arraste: as duas famílias que convivem na web (HTML5 e por ponteiro), a
// detecção de qual usar e a checagem de que algo mudou de fato.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/dom"
)

// DragOptions ajusta o arraste.
type DragOptions struct {
	// DropAt diz onde soltar sobre o alvo: "" (centro), "top" (25% do topo) ou
	// "bottom" (75%). Importa quando o alvo decide antes/depois pela posição.
	DropAt string
	// Tipo força a família do arraste: "html5" ou "ponteiro". Vazio detecta.
	Tipo string
	// Steps é quantos passos de mouse no caminho do arraste por ponteiro.
	Steps int
}

// paraLinha troca o alvo pela "linha" que o contém — o item de lista ou de
// tabela mais próximo.
//
// Descoberto no reorder do LinkedIn: mirar o parágrafo (≈20px, centralizado na
// linha de 48px) ou a linha inteira muda o ponto de soltura — e a biblioteca
// insere no índice da linha sob o ponteiro. Três tentativas erraram a posição
// por causa disso; mirando a linha, acertou de primeira.
func paraLinha(ctx context.Context, client *cdp.Client, session string, t *Target) {
	if t == nil || t.ObjectID == "" {
		return
	}
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function () {
			return this.closest ? (this.closest('li,tr,[role="listitem"],[role="row"]') || this) : this;
		}`,
		"returnByValue": false,
	}, session)
	if err != nil {
		return
	}
	var res struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.ObjectID == "" || res.Result.ObjectID == t.ObjectID {
		return
	}
	rect, err := dom.BoxOf(ctx, client, session, res.Result.ObjectID)
	if err != nil {
		return
	}
	t.ObjectID = res.Result.ObjectID
	t.Rect = rect
}

// assinaturaDe resume onde o elemento está (índice entre os irmãos + posição),
// para saber se o arraste mudou alguma coisa de fato.
func assinaturaDe(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (!this || !this.parentElement) return '';
			const irmaos = [...this.parentElement.children];
			const r = this.getBoundingClientRect();
			return irmaos.indexOf(this) + '@' + Math.round(r.left) + ',' + Math.round(r.top) + '/' + irmaos.length;
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

// Drag arrasta `from` até `to`.
//
// Duas famílias de arraste convivem na web e não se falam:
//
//   - HTML5 (`draggable`, dragstart/dragover/drop): mouse injetado NÃO inicia o
//     gesto — o Chrome só cria o dragstart a partir de entrada real do usuário.
//     Aqui o arraste é montado na página, com DataTransfer de verdade.
//   - por ponteiro (pointerdown/move/up movendo o elemento, ex.: dnd-kit): é o
//     inverso — o que funciona é mouse de verdade, e evento sintético é ignorado.
//
// A origem com `draggable="true"` (nela ou num ancestral) diz de qual família se
// trata. `tipo=ponteiro|html5` força, se a detecção errar.
func Drag(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) (string, bool, error) {
	// A linha, não o texto: é ela que define o ponto de soltura.
	paraLinha(ctx, client, session, from)
	paraLinha(ctx, client, session, to)

	// Guarda onde a origem estava, para poder dizer se algo mudou de verdade —
	// gesto que não pega costuma terminar em clique, sem aviso nenhum.
	antes := assinaturaDe(ctx, client, session, from.ObjectID)

	tipo := opts.Tipo
	if tipo == "" {
		tipo = dragKind(ctx, client, session, from.ObjectID)
	}
	var err error
	if tipo == "ponteiro" {
		err = dragPointer(ctx, client, session, from, to, opts, p)
	} else {
		tipo = "html5"
		err = dragHTML5(ctx, client, session, from, to, opts, p)
	}
	if err != nil {
		return tipo, false, err
	}

	mudou := true
	if antes != "" {
		if depois := assinaturaDe(ctx, client, session, from.ObjectID); depois != "" {
			mudou = antes != depois
		}
	}
	return tipo, mudou, nil
}

// dragKind diz de que família é o arraste: "html5" ou "ponteiro".
//
// O sinal é o atributo explícito `draggable="true"` — a propriedade `draggable`
// sozinha não serve, porque imagem e link já são arrastáveis por padrão e
// marcariam como HTML5 qualquer clique sobre eles.
func dragKind(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			return this.closest && this.closest('[draggable="true"]') ? 'html5' : 'ponteiro';
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return "html5"
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) == nil && res.Result.Value == "ponteiro" {
		return "ponteiro"
	}
	return "html5"
}

// dragHTML5 monta o arraste na página. É o caminho da família HTML5, que ignora
// mouse injetado: emitimos dragstart/dragenter/dragover/drop/dragend com um
// DataTransfer real, sobre o elemento que está sob o ponto de soltura (o evento
// sobe, então quem escuta no container também recebe).
func dragHTML5(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) error {
	fx, fy := from.ondeAgir()
	tx, ty := dropPoint(to, opts.DropAt)

	// O cursor passeia até o destino: quem olha precisa ver o arraste acontecer.
	_ = p.Spotlight(ctx, client, session, &from.Rect)
	_ = p.MoveCursor(ctx, client, session, fx, fy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	_ = p.Spotlight(ctx, client, session, &to.Rect)
	_ = p.MoveCursor(ctx, client, session, tx, ty)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}

	onde := opts.DropAt
	if onde == "" {
		onde = "center"
	}
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            from.ObjectID,
		"functionDeclaration": dragScript,
		"arguments": []any{
			map[string]any{"objectId": to.ObjectID},
			map[string]any{"value": onde},
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
		return fmt.Errorf("arraste não montou: %s", res.Result.Value)
	}
	time.Sleep(40 * time.Millisecond)
	return nil
}

// dragPointer arrasta com mouse de verdade: é o que a família por ponteiro
// entende (pointerdown/move/up). Se a página ignorar o gesto, sobra um clique —
// por isso este caminho só é usado quando a origem NÃO é `draggable`.
func dragPointer(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) error {
	if opts.Steps <= 0 {
		opts.Steps = 16
	}
	fx, fy := from.ondeAgir()
	tx, ty := dropPoint(to, opts.DropAt)

	_ = p.Spotlight(ctx, client, session, &from.Rect)
	_ = p.MoveCursor(ctx, client, session, fx, fy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent",
		map[string]any{"type": "mouseMoved", "x": fx, "y": fy}, session); err != nil {
		return err
	}
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mousePressed", "x": fx, "y": fy,
		"button": "left", "buttons": 1, "clickCount": 1,
	}, session); err != nil {
		return err
	}
	// Uma pausa antes de andar: biblioteca de ponteiro costuma armar o gesto no
	// pointerdown e só passar a acompanhar o movimento no quadro seguinte.
	time.Sleep(40 * time.Millisecond)

	for i := 1; i <= opts.Steps; i++ {
		x := fx + (tx-fx)*float64(i)/float64(opts.Steps)
		y := fy + (ty-fy)*float64(i)/float64(opts.Steps)
		if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type": "mouseMoved", "x": x, "y": y, "buttons": 1,
		}, session); err != nil {
			return err
		}
		time.Sleep(14 * time.Millisecond)
	}

	_ = p.Spotlight(ctx, client, session, &to.Rect)
	_ = p.MoveCursor(ctx, client, session, tx, ty)
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseReleased", "x": tx, "y": ty,
		"button": "left", "buttons": 0, "clickCount": 1,
	}, session); err != nil {
		return err
	}
	time.Sleep(60 * time.Millisecond)
	return nil
}

// dragScript emite a sequência de arraste sobre os elementos reais.
const dragScript = `function (alvo, onde) {
	const fonte = this;
	if (!fonte || !alvo) return 'sem origem ou destino';
	const ponto = (el) => {
		const b = el.getBoundingClientRect();
		let y = b.top + b.height / 2;
		if (onde === 'top') y = b.top + b.height * 0.25;
		else if (onde === 'bottom') y = b.top + b.height * 0.75;
		return { x: b.left + b.width / 2, y: y };
	};
	const pf = ponto(fonte);
	const pd = ponto(alvo);
	const dt = new DataTransfer();
	const dispara = (tipo, el, p) => el.dispatchEvent(new DragEvent(tipo, {
		bubbles: true, cancelable: true, composed: true, dataTransfer: dt,
		clientX: p.x, clientY: p.y, screenX: p.x, screenY: p.y,
	}));
	dispara('dragstart', fonte, pf);
	const sob = document.elementFromPoint(pd.x, pd.y) || alvo;
	dispara('dragenter', sob, pd);
	dispara('dragover', sob, pd);
	dispara('drop', sob, pd);
	dispara('dragend', fonte, pd);
	return 'ok';
}`

// dropPoint devolve onde soltar sobre o alvo.
func dropPoint(t *Target, at string) (float64, float64) {
	switch at {
	case "top", "topo":
		return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height*0.25
	case "bottom", "base":
		return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height*0.75
	default:
		return t.ondeAgir()
	}
}
