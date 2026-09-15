// Clicáveis que a árvore de acessibilidade não marca.
//
// Boa parte dos alvos de um app real não tem papel nenhum: é `div` com handler
// de clique e `cursor: pointer`. A leitura vem da árvore de acessibilidade, e
// não os vê — não viram linha nem alvo. Medido no laboratório v3: as conversas
// são `<div class="thread">`, e o `snap --refs` trazia seis alvos, nenhum deles
// uma conversa. O agente teve de adivinhar o seletor no CSS.
//
// Aqui uma passada no DOM os encontra e dá a cada um um seletor curto, que o
// agente usa direto em `click css=...` — sem depender de ref, que é por leitura.
//
// O critério é estreito de propósito: só container que **não tem alvo dentro**.
// Um card que embala botões já tem alvos, e listá-lo seria ruído; a linha que
// interessa é a que só existe como alvo — a conversa, o item da lista clicável.
// Com o critério largo (`cursor:pointer` puro) a mesma página dava 249
// candidatos; com este, três.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// Clickable é um alvo que a árvore não marca.
type Clickable struct {
	Selector string `json:"seletor"`
	Label    string `json:"rotulo"`
}

// maxClicaveis é quanto entra na leitura. É aviso, não inventário: passando
// disso, a página tem mais alvo sem papel do que a leitura deve carregar.
const maxClicaveis = 12

// clicaveisJS acha os containers clicáveis sem papel e devolve um seletor para
// cada — conferido contra a própria página antes de sair daqui, porque um
// seletor que não resolve não serve para nada.
const clicaveisJS = `(() => {
	const interativo = 'button,a,input,select,textarea,[role],[tabindex]';
	const candidatos = [...document.querySelectorAll('*')].filter(el => {
		if (getComputedStyle(el).cursor !== 'pointer') return false;
		if (el.matches(interativo)) return false;
		// Dentro de um alvo que já existe (o texto de um botão, por exemplo) não
		// é alvo novo: o span de "Gostei 20" tem cursor de ponteiro herdado e
		// virou item da lista até esta linha existir.
		if (el.closest && el.closest(interativo)) return false;
		if (el.querySelector(interativo)) return false;
		if (!el.getClientRects().length) return false;
		return (el.innerText || '').trim() !== '';
	});
	// Só o mais de fora: cursor é herdado, então os filhos de um container
	// clicável também parecem clicáveis, e listar todos repetiria o mesmo alvo.
	const dentro = new Set(candidatos);
	const raizes = candidatos.filter(el => {
		for (let p = el.parentElement; p; p = p.parentElement) if (dentro.has(p)) return false;
		return true;
	});
	const tenta = (el, s) => { try { return document.querySelector(s) === el ? s : ''; } catch (e) { return ''; } };
	const seletorDe = (el) => {
		if (el.id) { const s = tenta(el, '#' + el.id); if (s) return s; }
		const tag = el.tagName.toLowerCase();
		const cls = typeof el.className === 'string' ? el.className.trim().split(/\s+/).filter(Boolean) : [];
		// O atributo de dado vem antes da classe, e sozinho: div.thread.active
		// assa a classe de ESTADO no seletor, e ele quebra assim que a conversa
		// muda de ativa para outra.
		for (const attr of el.getAttributeNames()) {
			if (!attr.startsWith('data-')) continue;
			const comDado = '[' + attr + '="' + el.getAttribute(attr) + '"]';
			const sozinho = tenta(el, tag + comDado);
			if (sozinho) return sozinho;
			if (cls.length) {
				const comClasse = tenta(el, tag + '.' + cls.join('.') + comDado);
				if (comClasse) return comClasse;
			}
		}
		if (cls.length) { const s = tenta(el, tag + '.' + cls.join('.')); if (s) return s; }
		// Último recurso: caminho por posição, subindo até seis níveis.
		let caminho = tag, no = el, i = 0;
		while (no.parentElement && i < 6) {
			const irmaos = [...no.parentElement.children];
			caminho = no.tagName.toLowerCase() + ':nth-child(' + (irmaos.indexOf(no) + 1) + ')' + (i ? ' > ' + caminho : '');
			if (tenta(el, caminho)) return caminho;
			no = no.parentElement;
			i++;
		}
		return '';
	};
	const itens = raizes.map(el => ({
		seletor: seletorDe(el),
		rotulo: (el.innerText || '').trim().replace(/\s+/g, ' ').slice(0, 80),
	})).filter(c => c.seletor);
	return { itens: itens.slice(0, 40), total: itens.length };
})()`

// lerClicaveis roda a passada e devolve os alvos sem papel. Falha em silêncio: a
// leitura sem eles continua sendo leitura útil.
func lerClicaveis(ctx context.Context, client *cdp.Client, session string) ([]Clickable, int) {
	var res struct {
		Items []Clickable `json:"itens"`
		Total int         `json:"total"`
	}
	raw, err := dom.Eval(ctx, client, session, clicaveisJS)
	if err != nil {
		return nil, 0
	}
	if json.Unmarshal(raw, &res) != nil {
		return nil, 0
	}
	return res.Items, res.Total
}

// secaoDeClicaveis monta o bloco que entra no fim da leitura. Fica separado de
// quem o chama porque é a parte que dá para testar sem browser.
func secaoDeClicaveis(itens []Clickable, total int) string {
	if len(itens) == 0 {
		return ""
	}
	mostrados := itens
	if len(mostrados) > maxClicaveis {
		mostrados = mostrados[:maxClicaveis]
	}
	var b strings.Builder
	b.WriteString("-- clicáveis sem papel na árvore (a leitura não os marca; aqui o seletor):")
	for _, c := range mostrados {
		fmt.Fprintf(&b, "\n  %s — %q", c.Selector, c.Label)
	}
	if resto := total - len(mostrados); resto > 0 {
		fmt.Fprintf(&b, "\n  (+%d)", resto)
	}
	return b.String()
}
