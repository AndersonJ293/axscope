// Iframes na leitura.
//
// A árvore de acessibilidade vem por frame: a do frame principal mostra o iframe
// como uma linha só (`- Iframe`, sem ref e sem conteúdo), e o documento de
// dentro é outra árvore. Sem juntar as duas, a leitura diz que existe um iframe
// e não diz o que tem dentro — o agente não fica sabendo que há um botão ali.
//
// O CDP entrega cada árvore separada (`Accessibility.getFullAXTree` com
// `frameId`) e diz qual elemento é o dono de cada frame (`DOM.getFrameOwner`).
// Aqui as árvores viram uma só, penduradas no nó do iframe.
//
// Origem diferente (OOPIF) ainda fica de fora: essa árvore vive no processo do
// outro site, e alcançá-la exige sessão CDP própria por frame — outro trabalho,
// anotado em PENDENCIAS.md.
package browser

import (
	"context"
	"fmt"

	"github.com/ajunior/browser-use/internal/cdp"
)

// frameInfo é o frame como o Page.getFrameTree descreve (só o que usamos).
type frameInfo struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	ParentID string `json:"parentId"`
}

// arvoreDeFrames é o formato recursivo do Page.getFrameTree.
type arvoreDeFrames struct {
	Frame       frameInfo        `json:"frame"`
	ChildFrames []arvoreDeFrames `json:"childFrames"`
}

// temIframe diz se vale a pena ir buscar os frames. Sem iframe na árvore não há
// o que juntar, e a maioria das páginas não tem — então o custo fica com quem
// usa iframe.
func temIframe(nodes []axNode) bool {
	for i := range nodes {
		if frameRoles[nodes[i].Role.str()] {
			return true
		}
	}
	return false
}

// juntarFrames devolve a árvore do frame principal com a de cada frame filho
// pendurada no nó do iframe correspondente. Sem frame filho, devolve a mesma
// coisa que recebeu.
func juntarFrames(ctx context.Context, client *cdp.Client, session string, nodes []axNode) []axNode {
	frames := framesFilhos(ctx, client, session)
	if len(frames) == 0 {
		return nodes
	}
	out := append([]axNode(nil), nodes...)
	for i, f := range frames {
		dono, err := frameOwner(ctx, client, session, f.ID)
		if err != nil || dono == 0 {
			continue
		}
		pai := noPorBackend(out, dono)
		if pai == "" {
			continue
		}
		var t struct {
			Nodes []axNode `json:"nodes"`
		}
		if err := client.SendJSON(ctx, "Accessibility.getFullAXTree",
			map[string]any{"frameId": f.ID}, session, &t); err != nil || len(t.Nodes) == 0 {
			continue
		}
		out = append(out, enxertarFrame(t.Nodes, fmt.Sprintf("f%d:", i), pai)...)
	}
	return out
}

// framesFilhos lista os frames que não são o principal, do mais externo para o
// mais interno. A ordem importa: um frame de dentro só tem onde se pendurar
// depois que o de fora já entrou.
func framesFilhos(ctx context.Context, client *cdp.Client, session string) []frameInfo {
	var res struct {
		FrameTree arvoreDeFrames `json:"frameTree"`
	}
	if err := client.SendJSON(ctx, "Page.getFrameTree", map[string]any{}, session, &res); err != nil {
		return nil
	}
	var out []frameInfo
	var anda func(a arvoreDeFrames, raiz bool)
	anda = func(a arvoreDeFrames, raiz bool) {
		if !raiz && a.Frame.ID != "" {
			out = append(out, a.Frame)
		}
		for _, c := range a.ChildFrames {
			anda(c, false)
		}
	}
	anda(res.FrameTree, true)
	return out
}

// frameOwner devolve o backendNodeId do elemento que hospeda o frame.
func frameOwner(ctx context.Context, client *cdp.Client, session, frameID string) (int, error) {
	var res struct {
		BackendNodeID int `json:"backendNodeId"`
	}
	err := client.SendJSON(ctx, "DOM.getFrameOwner", map[string]any{"frameId": frameID}, session, &res)
	return res.BackendNodeID, err
}

// noPorBackend acha, na árvore montada até agora, o nó do elemento.
func noPorBackend(nodes []axNode, backend int) string {
	for i := range nodes {
		if nodes[i].BackendDOMNodeID == backend {
			return nodes[i].NodeID
		}
	}
	return ""
}

// enxertarFrame prepara a árvore de um frame para entrar na do principal.
//
// A raiz do frame sai: no frame principal ela viraria só mais uma linha
// (`RootWebArea` com o título do documento de dentro), e o que interessa é o
// conteúdo. São os filhos dela que se penduram no nó do iframe.
//
// O prefixo nos ids é obrigatório: cada árvore numera os nós a partir do próprio
// root, então os ids de dois frames colidem no mesmo mapa — e a leitura de
// dentro do iframe sairia misturada com a de fora.
func enxertarFrame(nodes []axNode, prefixo, pai string) []axNode {
	raiz := ""
	for i := range nodes {
		if nodes[i].ParentID == "" {
			raiz = nodes[i].NodeID
			break
		}
	}
	out := make([]axNode, 0, len(nodes))
	for _, n := range nodes {
		if n.NodeID == raiz {
			continue
		}
		n.NodeID = prefixo + n.NodeID
		switch n.ParentID {
		case "", raiz:
			n.ParentID = pai
		default:
			n.ParentID = prefixo + n.ParentID
		}
		filhos := make([]string, len(n.ChildIDs))
		for j, c := range n.ChildIDs {
			filhos[j] = prefixo + c
		}
		n.ChildIDs = filhos
		out = append(out, n)
	}
	return out
}
