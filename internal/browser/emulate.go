// Viewport emulation: test a layout at another size (a 360px phone) without
// resizing the real window.
package browser

import (
	"context"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Viewport is the device a tab is emulated as: CSS pixels, a device pixel ratio
// and whether it is a touch (mobile) device.
type Viewport struct {
	Width  int
	Height int
	Scale  float64
	Mobile bool
}

// SetViewport emulates a device viewport for the tab. It changes the layout (what
// `snap` reads) and the pixels `shot` captures, without touching the window.
func SetViewport(ctx context.Context, client *cdp.Client, session string, v Viewport) error {
	if v.Scale <= 0 {
		v.Scale = 1
	}
	_, err := client.Send(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             v.Width,
		"height":            v.Height,
		"deviceScaleFactor": v.Scale,
		"mobile":            v.Mobile,
	}, session)
	if err != nil {
		return err
	}
	return setTouch(ctx, client, session, v.Mobile)
}

// ResetViewport clears the override, returning the tab to the real viewport.
func ResetViewport(ctx context.Context, client *cdp.Client, session string) error {
	_, err := client.Send(ctx, "Emulation.clearDeviceMetricsOverride", map[string]any{}, session)
	if err != nil {
		return err
	}
	return setTouch(ctx, client, session, false)
}

func setTouch(ctx context.Context, client *cdp.Client, session string, on bool) error {
	params := map[string]any{"enabled": on}
	if on {
		params["maxTouchPoints"] = 5
	}
	_, err := client.Send(ctx, "Emulation.setTouchEmulationEnabled", params, session)
	return err
}
