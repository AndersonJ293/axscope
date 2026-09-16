// Domain presentation port: the browser asks for what to draw without knowing
// how. The concrete implementation is injected by the agent.
package browser

import (
	"context"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// Presenter draws the action for whoever watches the browser, without the domain
// depending on the presentation.
type Presenter interface {
	Install(ctx context.Context, client *cdp.Client, session string) error
	MoveCursor(ctx context.Context, client *cdp.Client, session string, x, y float64) error
	PressCursor(ctx context.Context, client *cdp.Client, session string, x, y float64, kind string) error
	Spotlight(ctx context.Context, client *cdp.Client, session string, rect *dom.Rect) error
	SetHUD(ctx context.Context, client *cdp.Client, session, tabs, label string) error
}
