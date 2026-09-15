// Sonda de diagnóstico: mede quais domínios CDP um endpoint implementa.
// Uso: go run ./cmd/cdpprobe ws://127.0.0.1:9223/
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

	probe := func(label, method string, params map[string]any, session string) {
		raw, err := c.SendTimeout(ctx, method, params, session, 6*time.Second)
		if err != nil {
			fmt.Printf("%-34s ERRO  %v\n", label, err)
			return
		}
		out := string(raw)
		if len(out) > 220 {
			out = out[:220] + "…"
		}
		if out == "{}" || out == "" {
			out = "(vazio)"
		}
		fmt.Printf("%-34s OK    %s\n", label, out)
	}

	probe("Browser.getVersion", "Browser.getVersion", nil, "")

	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if raw, err := c.Send(ctx, "Target.getTargets", map[string]any{}, ""); err == nil {
		_ = json.Unmarshal(raw, &targets)
		fmt.Printf("%-34s OK    %d targets\n", "Target.getTargets", len(targets.TargetInfos))
	} else {
		fmt.Printf("%-34s ERRO  %v\n", "Target.getTargets", err)
	}

	session := ""
	for _, t := range targets.TargetInfos {
		if t.Type == "page" {
			var res struct {
				SessionID string `json:"sessionId"`
			}
			if err := c.SendJSON(ctx, "Target.attachToTarget",
				map[string]any{"targetId": t.TargetID, "flatten": true}, "", &res); err == nil {
				session = res.SessionID
				fmt.Printf("attach %s -> session %s\n", t.Type, session)
			}
			break
		}
	}
	if session == "" {
		// Sem targets (modelo do Lightpanda): cria um e anexa.
		var res struct {
			TargetID string `json:"targetId"`
		}
		if err := c.SendJSON(ctx, "Target.createTarget",
			map[string]any{"url": "about:blank"}, "", &res); err != nil {
			fmt.Printf("%-34s ERRO  %v\n", "Target.createTarget", err)
			return
		}
		fmt.Printf("%-34s OK    %s\n", "Target.createTarget", res.TargetID)
		var att struct {
			SessionID string `json:"sessionId"`
		}
		if err := c.SendJSON(ctx, "Target.attachToTarget",
			map[string]any{"targetId": res.TargetID, "flatten": true}, "", &att); err != nil {
			fmt.Printf("%-34s ERRO  %v\n", "Target.attachToTarget", err)
			return
		}
		session = att.SessionID
		fmt.Printf("attach -> session %s\n", session)
	}
	if session == "" {
		fmt.Println("não consegui uma sessão de página")
		return
	}

	probe("Page.enable", "Page.enable", nil, session)
	probe("Runtime.enable", "Runtime.enable", nil, session)
	probe("Network.enable", "Network.enable", nil, session)
	probe("DOM.enable", "DOM.enable", nil, session)
	probe("Accessibility.enable", "Accessibility.enable", nil, session)
	probe("Page.navigate", "Page.navigate", map[string]any{"url": "https://example.com"}, session)
	time.Sleep(1500 * time.Millisecond)
	probe("Runtime.evaluate title", "Runtime.evaluate",
		map[string]any{"expression": "document.title", "returnByValue": true}, session)
	probe("Accessibility.getFullAXTree", "Accessibility.getFullAXTree", map[string]any{}, session)
	probe("DOM.getDocument", "DOM.getDocument", map[string]any{"depth": 1}, session)
	probe("Page.captureScreenshot", "Page.captureScreenshot", map[string]any{"format": "png"}, session)
	probe("Input.dispatchMouseEvent", "Input.dispatchMouseEvent",
		map[string]any{"type": "mouseMoved", "x": 10, "y": 10}, session)
}
