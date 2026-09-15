// Ponte com o overlay injetado (overlay.inject.js, embutido no binário).
package overlay

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/page"
)

// Source é o script do overlay, embutido no binário.
//
//go:embed overlay.inject.js
var Source string

// Rect é um retângulo em coordenadas de viewport (CSS px).
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Install registra o overlay para toda navegação futura e o instala agora.
func Install(ctx context.Context, c *cdp.Client, session string) error {
	if _, err := c.Send(ctx, "Page.addScriptToEvaluateOnNewDocument",
		map[string]any{"source": Source}, session); err != nil {
		return err
	}
	_, err := page.Eval(ctx, c, session, Source)
	return err
}

func args(values ...any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			b = []byte("null")
		}
		parts[i] = string(b)
	}
	return strings.Join(parts, ", ")
}

// call chama um método de window.__bu com guarda de existência.
func call(ctx context.Context, c *cdp.Client, session, method string, values ...any) error {
	expr := fmt.Sprintf("(window.__bu ? window.__bu.%s(%s) : null)", method, args(values...))
	_, err := page.Eval(ctx, c, session, expr)
	return err
}

// MoveCursor move o cursor renderizado até (x, y) no viewport.
func MoveCursor(ctx context.Context, c *cdp.Client, session string, x, y float64) error {
	return call(ctx, c, session, "cursor", x, y)
}

// Press anima cursor + ripple no ponto (o que a pessoa vê).
func Press(ctx context.Context, c *cdp.Client, session string, x, y float64, kind string) error {
	if kind == "" {
		kind = "left"
	}
	return call(ctx, c, session, "press", x, y, kind)
}

// Spotlight realça o retângulo do alvo; nil limpa.
//
// Desligado por padrão. O contorno no alvo poluía mais do que ajudava: ficava
// aceso depois da ação e, quando a página rolava, apontava para o nada. Quem
// quiser de volta liga com BROWSER_USE_DESTAQUE=1.
func Spotlight(ctx context.Context, c *cdp.Client, session string, rect *Rect) error {
	ligado, _ := strconv.Atoi(os.Getenv("BROWSER_USE_DESTAQUE"))
	if ligado <= 0 {
		return nil
	}
	if rect == nil {
		return call(ctx, c, session, "clearSpotlight")
	}
	return call(ctx, c, session, "spotlight", rect)
}

// SetHUD atualiza o HUD (abas + última ação).
func SetHUD(ctx context.Context, c *cdp.Client, session, tabs, label string) error {
	return call(ctx, c, session, "hud", map[string]string{"tabs": tabs, "label": label})
}

// SetVisible liga/desliga o overlay inteiro.
func SetVisible(ctx context.Context, c *cdp.Client, session string, visible bool) error {
	return call(ctx, c, session, "show", visible)
}
