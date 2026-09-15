// Estado de rolagem, lido do DOM.
//
// A árvore de acessibilidade não carrega rolagem: o CDP não serializa
// scrollTop/scrollHeight nela. Sem isso a leitura mostra as linhas e nunca onde
// se está nelas — "rolar até o item 777 de 1000" vira chute. A informação vem
// numa passada no DOM, junto com título e URL, para não custar ida e volta a
// mais: antes eram duas avaliações, agora é uma.
//
// Só o eixo vertical entra. O horizontal existe (barra de código, painel largo)
// e hoje fica de fora: reportar os dois inventaria um formato para um caso que
// ainda não mordeu. Quando morder, o lugar é aqui.
package browser

import (
	"context"
	"encoding/json"

	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/dom"
)

// Rolagem é onde uma área rolável está: quem rola, quanto já rolou e quanto
// ainda cabe.
type Rolagem struct {
	Alvo string `json:"alvo"`
	Pos  int    `json:"pos"`
	Max  int    `json:"max"`
}

// metaDaPagina é o que a leitura precisa saber da página além da árvore.
type metaDaPagina struct {
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Pagina   *Rolagem  `json:"pagina"`
	Rolagens []Rolagem `json:"rolagens"`
	Total    int       `json:"total"`
}

// lerMetaDaPagina busca título, URL e o estado de rolagem numa avaliação só.
// Falha em silêncio: leitura sem isso ainda é leitura útil — o que não pode é a
// tela não ser lida porque a página não deixou medir a rolagem.
func lerMetaDaPagina(ctx context.Context, client *cdp.Client, session string) metaDaPagina {
	var meta metaDaPagina
	raw, err := dom.Eval(ctx, client, session, metaDaPaginaJS)
	if err != nil {
		return meta
	}
	_ = json.Unmarshal(raw, &meta)
	return meta
}

// metaDaPaginaJS coleta o estado da página. As áreas roláveis saem nomeadas com
// um seletor curto, para o agente poder mirá-las por `css=` sem tradução.
const metaDaPaginaJS = `(() => {
	const curto = (el) => {
		if (el.id) return '#' + el.id;
		const tag = (el.tagName || '?').toLowerCase();
		const cls = [...(el.classList || [])].slice(0, 2);
		return cls.length ? tag + '.' + cls.join('.') : tag;
	};
	const doc = document.scrollingElement || document.documentElement;
	const sobraPagina = Math.round(doc.scrollHeight - doc.clientHeight);
	const pagina = { alvo: 'página', pos: Math.round(doc.scrollTop), max: sobraPagina };
	const rolagens = [];
	let total = sobraPagina > 1 ? 1 : 0;
	for (const el of document.querySelectorAll('*')) {
		if (el === doc || el === document.documentElement || el === document.body) continue;
		// O teste barato vem primeiro: quase todo elemento não rola, e o
		// getComputedStyle de todos custa caro. Só quem sobra paga.
		const sobra = el.scrollHeight - el.clientHeight;
		if (sobra <= 1) continue;
		if (!/(auto|scroll|overlay)/.test(getComputedStyle(el).overflowY)) continue;
		total++;
		rolagens.push({ alvo: curto(el), pos: Math.round(el.scrollTop), max: sobra });
	}
	// Maior primeiro: a área que rola mais é a que costuma importar para
	// "rolar até o item 777 de 1000", e o cabeçalho é curto por definição.
	rolagens.sort((a, b) => b.max - a.max);
	return { title: document.title, url: location.href, pagina: pagina, rolagens: rolagens.slice(0, 8), total: total };
})()`
