// DOM and page runtime access: one place for JavaScript evaluation, element
// measurement and scrolling, so callers do not duplicate Runtime/DOM calls.
package dom

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Rect is a rectangle in viewport coordinates (CSS px).
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Eval evaluates `expr` and returns the raw value (returnByValue).
func Eval(ctx context.Context, c *cdp.Client, session, expr string) (json.RawMessage, error) {
	return evaluate(ctx, c, session, expr, false)
}

// EvalAwait is Eval waiting for promises.
func EvalAwait(ctx context.Context, c *cdp.Client, session, expr string) (json.RawMessage, error) {
	return evaluate(ctx, c, session, expr, true)
}

func evaluate(ctx context.Context, c *cdp.Client, session, expr string, await bool) (json.RawMessage, error) {
	raw, err := c.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  await,
	}, session)
	if err != nil {
		return nil, err
	}
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		detail := res.ExceptionDetails.Text
		if detail == "" && res.ExceptionDetails.Exception != nil {
			detail = res.ExceptionDetails.Exception.Description
		}
		if detail == "" {
			detail = "error while evaluating on the page"
		}
		return nil, fmt.Errorf("%s", detail)
	}
	return res.Result.Value, nil
}

// EvalString is Eval deserializing the value into a string.
func EvalString(ctx context.Context, c *cdp.Client, session, expr string) (string, error) {
	raw, err := Eval(ctx, c, session, expr)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

// EvalObject evaluates `expr` and returns the element's objectId (empty if null).
func EvalObject(ctx context.Context, c *cdp.Client, session, expr string) (string, error) {
	raw, err := c.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": false,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Subtype  string `json:"subtype"`
			ObjectID string `json:"objectId"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	return res.Result.ObjectID, nil
}

// BoxOf returns the element's rectangle in viewport px.
func BoxOf(ctx context.Context, c *cdp.Client, session, objectID string) (Rect, error) {
	raw, err := c.Send(ctx, "DOM.getBoxModel", map[string]any{"objectId": objectID}, session)
	if err == nil {
		var model struct {
			Model struct {
				Content []float64 `json:"content"`
			} `json:"model"`
		}
		if json.Unmarshal(raw, &model) == nil && len(model.Model.Content) == 8 {
			q := model.Model.Content
			xs := []float64{q[0], q[2], q[4], q[6]}
			ys := []float64{q[1], q[3], q[5], q[7]}
			minX, maxX := xs[0], xs[0]
			minY, maxY := ys[0], ys[0]
			for _, v := range xs {
				if v < minX {
					minX = v
				}
				if v > maxX {
					maxX = v
				}
			}
			for _, v := range ys {
				if v < minY {
					minY = v
				}
				if v > maxY {
					maxY = v
				}
			}
			return Rect{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}, nil
		}
	}

	// Fallback: getBoundingClientRect in the element's context.
	raw, err = c.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			const r = this.getBoundingClientRect();
			return { x: r.x, y: r.y, width: r.width, height: r.height };
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return Rect{}, err
	}
	var res struct {
		Result struct {
			Value Rect `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return Rect{}, err
	}
	if res.Result.Value.Width == 0 && res.Result.Value.Height == 0 {
		return Rect{}, fmt.Errorf("element has no size")
	}
	return res.Result.Value, nil
}

// ScrollTo makes sure the element is visible in the viewport, with no animation of its own.
func ScrollTo(ctx context.Context, c *cdp.Client, session, objectID string) error {
	_, err := c.Send(ctx, "DOM.scrollIntoViewIfNeeded",
		map[string]any{"objectId": objectID}, session)
	return err
}
