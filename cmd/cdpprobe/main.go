// CDP engine diagnostic probe: reports the endpoint's CDP domains, independent
// tabs, geometry and ref support. Usage: go run ./cmd/cdpprobe [ws://host:port/]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
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

	section("1. CDP domains")
	probe(ctx, c, "Browser.getVersion", "Browser.getVersion", nil, "")

	section("2. tabs (two connections = two sessions)")
	c2, err := cdp.Dial(ctx, url, 10*time.Second)
	if err != nil {
		fmt.Println("second connection failed:", err)
	} else {
		defer c2.Close()
	}
	t1, s1 := createPage(ctx, c, "", "https://example.com")
	page2 := c
	if c2 != nil {
		page2 = c2
	}
	t2, s2 := createPage(ctx, page2, "", "https://www.iana.org/help/example-domains")
	fmt.Printf("connection 1: target=%s session=%s\n", t1, s1)
	fmt.Printf("connection 2: target=%s session=%s\n", t2, s2)
	time.Sleep(2 * time.Second)
	title1 := evalString(ctx, c, s1, "document.title")
	href1 := evalString(ctx, c, s1, "location.href")
	title2 := evalString(ctx, page2, s2, "document.title")
	href2 := evalString(ctx, page2, s2, "location.href")
	fmt.Printf("session 1: %q  %s\n", title1, href1)
	fmt.Printf("session 2: %q  %s\n", title2, href2)
	if title1 != "" && title2 != "" && title1 != title2 {
		fmt.Println("=> YES: two independent sessions (different titles)")
	} else {
		fmt.Println("=> NO: could not get two independent sessions")
	}

	section("3. geometry (the basis of action by coordinate)")
	pageProbe := func(label, expr string) {
		v := evalString(ctx, c, s1, expr)
		fmt.Printf("%-28s %s\n", label, v)
	}
	pageProbe("innerWidth/Height", "JSON.stringify({w:innerWidth,h:innerHeight})")
	pageProbe("getBoundingClientRect of <a>", `(() => {
		const el = document.querySelector('a');
		if (!el) return 'no <a>';
		const r = el.getBoundingClientRect();
		return JSON.stringify({x:r.x,y:r.y,width:r.width,height:r.height});
	})()`)

	section("4. accessibility tree and refs")
	var doc struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := c.SendJSON(ctx, "DOM.getDocument", map[string]any{"depth": 1}, s1, &doc); err != nil {
		fmt.Println("DOM.getDocument ERROR:", err)
	}
	var q struct {
		NodeID int `json:"nodeId"`
	}
	if err := c.SendJSON(ctx, "DOM.querySelector",
		map[string]any{"nodeId": doc.Root.NodeID, "selector": "a"}, s1, &q); err != nil {
		fmt.Println("DOM.querySelector ERROR:", err)
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
				fmt.Println("DOM.getBoxModel ERROR:", err)
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
		if err := json.Unmarshal(raw, &tree); err != nil {
			fmt.Println("Accessibility.getFullAXTree decode ERROR:", err)
		}
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
		fmt.Printf("AX: %d nodes, %d with backendDOMNodeId\n", len(tree.Nodes), withBackend)
		if firstBackend != 0 {
			var res struct {
				Object struct {
					ObjectID string `json:"objectId"`
				} `json:"object"`
			}
			if err := c.SendJSON(ctx, "DOM.resolveNode",
				map[string]any{"backendNodeId": firstBackend}, s1, &res); err != nil {
				fmt.Println("DOM.resolveNode ERROR:", err)
			} else if res.Object.ObjectID == "" {
				fmt.Println("DOM.resolveNode            no objectId => does NOT resolve the ref")
			} else {
				fmt.Println("DOM.resolveNode            OK => refs work")
			}
		}
	} else {
		fmt.Println("Accessibility.getFullAXTree ERROR:", err)
	}
}

func section(title string) { fmt.Printf("\n===== %s =====\n", title) }

func probe(ctx context.Context, c *cdp.Client, label, method string, params map[string]any, session string) {
	raw, err := c.SendTimeout(ctx, method, params, session, 6*time.Second)
	if err != nil {
		fmt.Printf("%-30s ERROR %v\n", label, err)
		return
	}
	out := string(raw)
	if len(out) > 160 {
		out = out[:160] + "…"
	}
	if out == "{}" || out == "" {
		out = "(empty)"
	}
	fmt.Printf("%-30s OK    %s\n", label, out)
}

// createPage creates a target in a context and returns (targetId, sessionId).
func createPage(ctx context.Context, c *cdp.Client, browserContextID, url string) (string, string) {
	params := map[string]any{"url": "about:blank"}
	if browserContextID != "" {
		params["browserContextId"] = browserContextID
	}
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := c.SendJSON(ctx, "Target.createTarget", params, "", &res); err != nil {
		fmt.Println("Target.createTarget ERROR:", err)
		return "", ""
	}
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.SendJSON(ctx, "Target.attachToTarget",
		map[string]any{"targetId": res.TargetID, "flatten": true}, "", &att); err != nil {
		fmt.Println("Target.attachToTarget ERROR:", err)
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

// evalString prints the value of an expression. It uses the same runtime access
// as the driver, so as not to keep a second evaluation only for diagnostics.
func evalString(ctx context.Context, c *cdp.Client, session, expr string) string {
	raw, err := dom.Eval(ctx, c, session, expr)
	if err != nil {
		return "ERROR: " + err.Error()
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "null"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
