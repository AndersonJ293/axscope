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
	"sync"
	"time"

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

// defaultGlideMs caps a cursor glide. A hop takes 40ms and a long move up to
// the cap: enough for whoever watches to follow, short enough not to slow an
// agent down. AXSCOPE_CURSOR_DELAY overrides it; 0 jumps.
const defaultGlideMs = 80

func glideCap() int {
	ms := defaultGlideMs
	if v := os.Getenv("AXSCOPE_CURSOR_DELAY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ms = n
		}
	}
	return max(ms, 0)
}

// cursors remembers where each tab's cursor was last drawn. A navigation
// rebuilds the overlay, and without this the cursor would pop in at the target
// instead of gliding from where the person last saw it.
var cursors sync.Map // session -> [2]float64

type glideOpts struct {
	From *[2]float64 `json:"from,omitempty"`
	Max  int         `json:"max"`
}

func lastPoint(session string) *[2]float64 {
	if v, ok := cursors.Load(session); ok {
		p := v.([2]float64)
		return &p
	}
	return nil
}

// glide runs a cursor method and waits for the move it reports, so the real
// input lands when the drawn cursor arrives — no fixed pause.
func glide(ctx context.Context, c *cdp.Client, session, method string, x, y float64, extra ...any) error {
	opts := glideOpts{From: lastPoint(session), Max: glideCap()}
	values := append([]any{x, y}, extra...)
	values = append(values, opts)
	expr := fmt.Sprintf("(window.__axscope ? window.__axscope.%s(%s) : 0)", method, args(values...))
	raw, err := dom.Eval(ctx, c, session, expr)
	if err != nil {
		return err
	}
	cursors.Store(session, [2]float64{x, y})
	var ms float64
	if json.Unmarshal(raw, &ms) == nil && ms > 0 {
		wait(ctx, time.Duration(ms)*time.Millisecond)
	}
	return nil
}

func wait(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// MoveCursor glides the rendered cursor to (x, y) in the viewport.
func (Presenter) MoveCursor(ctx context.Context, c *cdp.Client, session string, x, y float64) error {
	return glide(ctx, c, session, "cursor", x, y)
}

// PressCursor glides to the point and presses there (what the person sees);
// it returns when the cursor has arrived.
func (Presenter) PressCursor(ctx context.Context, c *cdp.Client, session string, x, y float64, kind string) error {
	if kind == "" {
		kind = "left"
	}
	return glide(ctx, c, session, "press", x, y, kind)
}

// Spotlight highlights the target rectangle; nil clears it. Off by default,
// since the outline could stay lit after the action; AXSCOPE_SPOTLIGHT=1 turns it on.
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

// SetHUD updates the HUD (tabs + last action). It runs after every command, so
// it also puts the cursor back where it was when a navigation rebuilt the
// overlay: the cursor stays on screen instead of vanishing until the next click.
func (Presenter) SetHUD(ctx context.Context, c *cdp.Client, session, tabs, label string) error {
	hud := args(map[string]string{"tabs": tabs, "label": label})
	expr := fmt.Sprintf("(window.__axscope ? window.__axscope.hud(%s) : null)", hud)
	if p := lastPoint(session); p != nil {
		expr = fmt.Sprintf("(window.__axscope ? (window.__axscope.cursor(%s), window.__axscope.hud(%s)) : null)",
			args(p[0], p[1], glideOpts{From: p}), hud)
	}
	_, err := dom.Eval(ctx, c, session, expr)
	return err
}
