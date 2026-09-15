// Screenshot: the whole page or only what is in view.
package browser

import (
	"context"
	"encoding/json"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Screenshot captures the page and returns the PNG bytes.
func Screenshot(ctx context.Context, client *cdp.Client, session string, fullPage bool) ([]byte, error) {
	params := map[string]any{"format": "png", "fromSurface": true}
	if fullPage {
		params["captureBeyondViewport"] = true
	}
	raw, err := client.SendTimeout(ctx, "Page.captureScreenshot", params, session, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var res struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return decodeBase64(res.Data)
}
