// Sonda de diagnóstico de engines CDP.
//
// Responde, com medição e não com suposição:
//  1. quais domínios CDP o endpoint implementa;
//  2. se ele suporta várias páginas/targets independentes (abas headless);
//  3. se há geometria (getBoxModel / getBoundingClientRect) — sem isso, ação
//     por coordenada não existe;
//  4. se a árvore de acessibilidade traz backendDOMNodeId e se dá para
//     resolver de volta em nó do DOM (é o que nossas `ref` exigem).
//
// Uso: go run ./cmd/cdpprobe [ws://host:porta/]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ajunior/browser-use/internal/cdp"
)

func main() {
	url := "ws://127.0.0.1:9223/"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}
	ctx := context.Background()
	c, err := cdp.Dial(ctx, url, 10*time.Second)
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer c.Close()

	section("1. domínios CDP")
	probe(ctx, c, "Browser.getVersion", "Browser.getVersion", nil, "")

	section("2. abas (duas conexões = duas sessões)")
	c2, err := cdp.Dial(ctx, url, 10*time.Second)
	if err != nil {
		fmt.Println("segunda conexão falhou:", err)
	} else {
		defer c2.Close()
	}
	t1, s1 := createPage(ctx, c, "", "https://example.com")
	page2 := c
	if c2 != nil {
		page2 = c2
	}
	t2, s2 := createPage(ctx, page2, "", "https://www.iana.org/help/example-domains")
	fmt.Printf("conexão 1: target=%s session=%s\n", t1, s1)
	fmt.Printf("conexão 2: target=%s session=%s\n", t2, s2)
	time.Sleep(2 * time.Second)
	title1 := evalString(ctx, c, s1, "document.title")
	href1 := evalString(ctx, c, s1, "location.href")
	title2 := evalString(ctx, page2, s2, "document.title")
	href2 := evalString(ctx, page2, s2, "location.href")
	fmt.Printf("sessão 1: %q  %s\n", title1, href1)
	fmt.Printf("sessão 2: %q  %s\n", title2, href2)
	if title1 != "" && title2 != "" && title1 != title2 {
		fmt.Println("=> SIM: duas sessões independentes (títulos diferentes)")
	} else {
		fmt.Println("=> NÃO: não consegui duas sessões independentes")
	}

	section("3. geometria (base da ação por coordenada)")
	pageProbe := func(label, expr string) {
		v := evalString(ctx, c, s1, expr)
		fmt.Printf("%-28s %s\n", label, v)
	}
	pageProbe("innerWidth/Height", "JSON.stringify({w:innerWidth,h:innerHeight})")
	pageProbe("getBoundingClientRect do <a>", `(() => {
		const el = document.querySelector('a');
		if (!el) return 'sem <a>';
		const r = el.getBoundingClientRect();
		return JSON.stringify({x:r.x,y:r.y,width:r.width,height:r.height});
	})()`)

	section("4. árvore de acessibilidade e refs")
	var doc struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := c.SendJSON(ctx, "DOM.getDocument", map[string]any{"depth": 1}, s1, &doc); err != nil {
		fmt.Println("DOM.getDocument ERRO:", err)
	}
	var q struct {
		NodeID int `json:"nodeId"`
	}
	if err := c.SendJSON(ctx, "DOM.querySelector",
		map[string]any{"nodeId": doc.Root.NodeID, "selector": "a"}, s1, &q); err != nil {
		fmt.Println("DOM.querySelector ERRO:", err)
	} else {
		fmt.Printf("DOM.querySelector('a')     nodeId=%d\n", q.NodeID)
		if q.NodeID != 0 {
			var box struct {
				Model struct {
					Content []float64 `json:"content"`
				} `json:"model"`
			}
			if err := c.SendJSON(ctx, "DOM.getBoxModel",
				map[string]any{"nodeId": q.NodeID}, s1, &box); err != nil {
				fmt.Println("DOM.getBoxModel ERRO:", err)
			} else {
				fmt.Printf("DOM.getBoxModel            %d coords\n", len(box.Model.Content))
			}
		}
	}

	var tree struct {
		Nodes []struct {
			Role             struct{ Value string } `json:"role"`
			BackendDOMNodeID int                    `json:"backendDOMNodeId"`
		} `json:"nodes"`
	}
	if raw, err := c.Send(ctx, "Accessibility.getFullAXTree", map[string]any{}, s1); err == nil {
		_ = json.Unmarshal(raw, &tree)
		withBackend := 0
		roles := map[string]int{}
		var firstBackend int
		for _, n := range tree.Nodes {
			if n.BackendDOMNodeID != 0 {
				withBackend++
				if firstBackend == 0 {
					firstBackend = n.BackendDOMNodeID
				}
			}
			roles[n.Role.Value]++
		}
		fmt.Printf("AX: %d nós, %d com backendDOMNodeId\n", len(tree.Nodes), withBackend)
		if firstBackend != 0 {
			var res struct {
				Object struct {
					ObjectID string `json:"objectId"`
				} `json:"object"`
			}
			if err := c.SendJSON(ctx, "DOM.resolveNode",
				map[string]any{"backendNodeId": firstBackend}, s1, &res); err != nil {
				fmt.Println("DOM.resolveNode ERRO:", err)
			} else if res.Object.ObjectID == "" {
				fmt.Println("DOM.resolveNode            sem objectId => NÃO resolve a ref")
			} else {
				fmt.Println("DOM.resolveNode            OK => refs funcionam")
			}
		}
	} else {
		fmt.Println("Accessibility.getFullAXTree ERRO:", err)
	}
}

func section(title string) { fmt.Printf("\n===== %s =====\n", title) }

func probe(ctx context.Context, c *cdp.Client, label, method string, params map[string]any, session string) {
	raw, err := c.SendTimeout(ctx, method, params, session, 6*time.Second)
	if err != nil {
		fmt.Printf("%-30s ERRO  %v\n", label, err)
		return
	}
	out := string(raw)
	if len(out) > 160 {
		out = out[:160] + "…"
	}
	if out == "{}" || out == "" {
		out = "(vazio)"
	}
	fmt.Printf("%-30s OK    %s\n", label, out)
}

// createPage cria um target num contexto e devolve (targetId, sessionId).
func createPage(ctx context.Context, c *cdp.Client, browserContextID, url string) (string, string) {
	params := map[string]any{"url": "about:blank"}
	if browserContextID != "" {
		params["browserContextId"] = browserContextID
	}
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := c.SendJSON(ctx, "Target.createTarget", params, "", &res); err != nil {
		fmt.Println("Target.createTarget ERRO:", err)
		return "", ""
	}
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.SendJSON(ctx, "Target.attachToTarget",
		map[string]any{"targetId": res.TargetID, "flatten": true}, "", &att); err != nil {
		fmt.Println("Target.attachToTarget ERRO:", err)
		return res.TargetID, ""
	}
	for _, m := range []string{"Page.enable", "Runtime.enable", "DOM.enable", "Accessibility.enable"} {
		_, _ = c.SendTimeout(ctx, m, nil, att.SessionID, 5*time.Second)
	}
	if url != "" {
		_, _ = c.SendTimeout(ctx, "Page.navigate", map[string]any{"url": url}, att.SessionID, 15*time.Second)
	}
	return res.TargetID, att.SessionID
}

func evalString(ctx context.Context, c *cdp.Client, session, expr string) string {
	raw, err := c.SendTimeout(ctx, "Runtime.evaluate",
		map[string]any{"expression": expr, "returnByValue": true}, session, 8*time.Second)
	if err != nil {
		return "ERRO: " + err.Error()
	}
	var res struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "ERRO json"
	}
	if res.Result.Value == nil {
		return "null"
	}
	if s, ok := res.Result.Value.(string); ok {
		return s
	}
	b, _ := json.Marshal(res.Result.Value)
	return string(b)
}
