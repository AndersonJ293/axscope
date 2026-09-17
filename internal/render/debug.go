// Debug logging for the best-effort presentation calls: the domain ignores
// their errors on purpose, which keeps it clean but hides real failures while
// developing.
package render

import (
	"context"
	"log"
	"os"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// Logged wraps a Presenter and reports the calls that fail when AXSCOPE_DEBUG is
// on. The calls stay best-effort: the error is still returned for the caller to
// discard, and nothing is logged by default.
type Logged struct {
	browser.Presenter
}

func debugEnabled() bool {
	switch os.Getenv("AXSCOPE_DEBUG") {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

func (l Logged) report(op string, err error) error {
	if err != nil && debugEnabled() {
		log.Printf("presenter %s: %v", op, err)
	}
	return err
}

func (l Logged) Install(ctx context.Context, c *cdp.Client, session string) error {
	return l.report("install", l.Presenter.Install(ctx, c, session))
}

func (l Logged) MoveCursor(ctx context.Context, c *cdp.Client, session string, x, y float64) error {
	return l.report("cursor", l.Presenter.MoveCursor(ctx, c, session, x, y))
}

func (l Logged) PressCursor(ctx context.Context, c *cdp.Client, session string, x, y float64, kind string) error {
	return l.report("press", l.Presenter.PressCursor(ctx, c, session, x, y, kind))
}

func (l Logged) Spotlight(ctx context.Context, c *cdp.Client, session string, rect *dom.Rect) error {
	return l.report("spotlight", l.Presenter.Spotlight(ctx, c, session, rect))
}

func (l Logged) SetHUD(ctx context.Context, c *cdp.Client, session, tabs, label string) error {
	return l.report("hud", l.Presenter.SetHUD(ctx, c, session, tabs, label))
}
