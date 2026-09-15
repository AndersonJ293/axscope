// Porta de apresentação do domínio: o browser pede o que desenhar sem saber
// como. A implementação concreta (cursor, HUD, destaque) é injetada pelo agent.
package browser

import (
	"context"

	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/dom"
)

// Presenter desenha a ação para quem olha o navegador. Sem isto o domínio
// dependeria da apresentação — e a apresentação é detalhe de uma superfície.
type Presenter interface {
	Install(ctx context.Context, client *cdp.Client, session string) error
	MoveCursor(ctx context.Context, client *cdp.Client, session string, x, y float64) error
	Press(ctx context.Context, client *cdp.Client, session string, x, y float64, kind string) error
	Spotlight(ctx context.Context, client *cdp.Client, session string, rect *dom.Rect) error
	SetHUD(ctx context.Context, client *cdp.Client, session, tabs, label string) error
}
