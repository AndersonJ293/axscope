// Browser presentation: cursor, ripple, HUD and spotlight, injected into the
// page by the embedded script. Implements browser.Presenter.
package render

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// source is the presentation script, embedded in the binary.
//
//go:embed render.inject.js
var source string

// Presenter draws on the page viewport.
type Presenter struct{}

var _ browser.Presenter = Presenter{}

// Install registers the script for all future navigations and installs it now.
func (Presenter) Install(ctx context.Context, c *cdp.Client, session string) error {
	if _, err := c.Send(ctx, "Page.addScriptToEvaluateOnNewDocument",
		map[string]any{"source": source}, session); err != nil {
		return err
	}
	_, err := dom.Eval(ctx, c, session, source)
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

// call calls a window.__axscope method with an existence guard.
func call(ctx context.Context, c *cdp.Client, session, method string, values ...any) error {
	expr := fmt.Sprintf("(window.__axscope ? window.__axscope.%s(%s) : null)", method, args(values...))
	_, err := dom.Eval(ctx, c, session, expr)
	return err
}

// MoveCursor moves the rendered cursor to (x, y) in the viewport.
func (Presenter) MoveCursor(ctx context.Context, c *cdp.Client, session string, x, y float64) error {
	return call(ctx, c, session, "cursor", x, y)
}

// Press animates cursor + ripple at the point (what the person sees).
func (Presenter) Press(ctx context.Context, c *cdp.Client, session string, x, y float64, kind string) error {
	if kind == "" {
		kind = "left"
	}
	return call(ctx, c, session, "press", x, y, kind)
}

// Spotlight highlights the target rectangle; nil clears it.
//
// Off by default. The outline on the target polluted more than it helped: it
// stayed lit after the action and, when the page scrolled, pointed at nothing.
// Anyone who wants it back turns it on with AXSCOPE_SPOTLIGHT=1.
func (Presenter) Spotlight(ctx context.Context, c *cdp.Client, session string, rect *dom.Rect) error {
	enabled, _ := strconv.Atoi(os.Getenv("AXSCOPE_SPOTLIGHT"))
	if enabled <= 0 {
		return nil
	}
	if rect == nil {
		return call(ctx, c, session, "clearSpotlight")
	}
	return call(ctx, c, session, "spotlight", rect)
}

// SetHUD updates the HUD (tabs + last action).
func (Presenter) SetHUD(ctx context.Context, c *cdp.Client, session, tabs, label string) error {
	return call(ctx, c, session, "hud", map[string]string{"tabs": tabs, "label": label})
}
