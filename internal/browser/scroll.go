// Rolagem: a página, ou o container de um alvo. Vai em passos, para quem olha
// acompanhar o movimento em vez de a página pular de uma vez.
package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Scroll rola o viewport por (dx, dy). Vai em passos, para quem olha acompanhar
// o movimento em vez de a página pular de uma vez.
//
// Rola pelo scroller sob o centro da tela, e não por roda de mouse: a roda
// depende de quem está sob o ponteiro (numa página com caixa de rolagem no meio
// do caminho, ela rola o container errado) e o ack dela pelo chrome.debugger às
// vezes não volta — medido: 30s de timeout sem rolar nada.
//
// Depois de rolar, avisa a página com um evento de scroll quando a aba está
// oculta: nessa condição o navegador segura a entrega (ela depende do ciclo de
// quadros, que não roda escondido) e quem redesenha no scroll — lista
// virtualizada, carregamento preguiçoso — nunca fica sabendo que rolou. Foi
// assim que a lista virtualizada do laboratório ficou com o DOM parado em outro
// ponto enquanto a posição já era a certa.
//
// Devolve onde parou, no formato "<quem> <posicao>/<maximo>": a resposta diz o
// que a rolagem alcançou, não o que foi pedido — com scroll-snap, ou pedindo
// além do limite, os dois diferem. `pagina` força o documento, para quando o
// alvo está aninhado e o que se quer rolar é a página.
func Scroll(ctx context.Context, client *cdp.Client, session string, dx, dy float64, pagina bool) (string, error) {
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    fmt.Sprintf("(%s)(%v, %v, %v)", scrollPassos, dx, dy, pagina),
		"returnByValue": true,
		"awaitPromise":  true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.Value == "" {
		return "", fmt.Errorf("não consegui rolar")
	}
	return res.Result.Value, nil
}

// ScrollTarget rola o container do alvo — o próprio, se ele rola, ou o
// ancestral rolável mais próximo; sem nenhum, o documento. Devolve onde parou,
// como o Scroll.
func ScrollTarget(ctx context.Context, client *cdp.Client, session, objectID string, dx, dy float64) (string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": scrollDoAlvo,
		"arguments": []any{
			map[string]any{"value": dx},
			map[string]any{"value": dy},
		},
		"returnByValue": true,
		"awaitPromise":  true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.Value == "" {
		return "", fmt.Errorf("não consegui rolar o alvo")
	}
	return res.Result.Value, nil
}

// jsDescreveRolagem é colado nos dois scripts de rolagem: descreve onde parou e,
// no fim da página com a aba oculta, avisa do que isso impede. O aviso entra só
// no fim — é quando a ausência de conteúdo novo intriga —, e não em toda
// rolagem, que viraria ruído num caso que é o normal aqui.
const jsDescreveRolagem = `
	const descreve = (el) => {
		const nome = el === (document.scrollingElement || document.documentElement) ? 'página'
			: (el.id ? '#' + el.id : el.tagName.toLowerCase());
		const fim = el.scrollTop >= (el.scrollHeight - el.clientHeight) - 1;
		const nota = (fim && document.hidden)
			? ' — fim, e a aba está oculta: o que carrega por IntersectionObserver não dispara (use axscope tab <n> --focus)'
			: '';
		return nome + ' ' + Math.round(el.scrollTop) + '/' + Math.round(el.scrollHeight - el.clientHeight) + nota;
	};`

// scrollDoAlvo rola o container do elemento, em passos.
const scrollDoAlvo = `function (dx, dy) {` + jsDescreveRolagem + `
	const rola = (el) => {
		if (!el || !el.scrollHeight) return false;
		const st = getComputedStyle(el);
		return /(auto|scroll|overlay)/.test(st.overflowY) && el.scrollHeight > el.clientHeight + 1;
	};
	let rolavel = null;
	if (rola(this)) rolavel = this;
	else {
		let c = this && this.parentElement;
		while (c) { if (rola(c)) { rolavel = c; break; } c = c.parentElement; }
	}
	if (!rolavel) rolavel = document.scrollingElement || document.documentElement;
	const deX = rolavel.scrollLeft, deY = rolavel.scrollTop;
	const passos = (document.hidden || !rolavel.scrollHeight) ? 1 : 6;
	let i = 0;
	return new Promise((pronto) => {
		const passo = () => {
			i++;
			rolavel.scrollTo({
				left: deX + dx * (i / passos),
				top: deY + dy * (i / passos),
				behavior: 'instant',
			});
			if (i < passos) { setTimeout(passo, 35); return; }
			if (document.hidden) {
				rolavel.dispatchEvent(new Event('scroll'));
				if (rolavel === (document.scrollingElement || document.documentElement)) window.dispatchEvent(new Event('scroll'));
			}
			pronto(descreve(rolavel));
		};
		passo();
	});
}`

// scrollPassos rola o elemento rolável sob o centro da tela, em passos; com
// `pagina`, rola o documento.
const scrollPassos = `function (dx, dy, pagina) {` + jsDescreveRolagem + `
	let rolavel = document.scrollingElement || document.documentElement;
	if (!pagina) {
		const cx = Math.round(innerWidth / 2), cy = Math.round(innerHeight / 2);
		const alvo = document.elementFromPoint(cx, cy);
		if (alvo) {
			let c = alvo;
			while (c) {
				const st = getComputedStyle(c);
				if (/(auto|scroll|overlay)/.test(st.overflowY) && c.scrollHeight > c.clientHeight + 1) { rolavel = c; break; }
				c = c.parentElement;
			}
		}
	}
	const deX = rolavel.scrollLeft, deY = rolavel.scrollTop;
	// Aba em segundo plano estrangula setTimeout, e animar para ninguém só
	// deixa a rolagem lenta: escondida, vai de uma vez.
	const passos = document.hidden ? 1 : 6;
	let i = 0;
	return new Promise((pronto) => {
		const passo = () => {
			i++;
			rolavel.scrollTo({
				left: deX + dx * (i / passos),
				top: deY + dy * (i / passos),
				behavior: 'instant',
			});
			if (i < passos) { setTimeout(passo, 35); return; }
			if (document.hidden) {
				rolavel.dispatchEvent(new Event('scroll'));
				if (rolavel === (document.scrollingElement || document.documentElement)) window.dispatchEvent(new Event('scroll'));
			}
			pronto(descreve(rolavel));
		};
		passo();
	});
}`
