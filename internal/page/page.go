// Avaliação de JavaScript no contexto principal da página.
package page

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ajunior/browser-use/internal/cdp"
)

// Eval avalia `expr` e devolve o valor bruto (returnByValue).
func Eval(ctx context.Context, c *cdp.Client, session, expr string) (json.RawMessage, error) {
	return eval(ctx, c, session, expr, false)
}

// EvalAwait é Eval esperando promessas.
func EvalAwait(ctx context.Context, c *cdp.Client, session, expr string) (json.RawMessage, error) {
	return eval(ctx, c, session, expr, true)
}

func eval(ctx context.Context, c *cdp.Client, session, expr string, await bool) (json.RawMessage, error) {
	raw, err := c.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  await,
		"userGesture":   true,
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
		if res.ExceptionDetails.Exception != nil && res.ExceptionDetails.Exception.Description != "" {
			detail = res.ExceptionDetails.Exception.Description
		}
		if detail == "" {
			detail = "erro ao avaliar na página"
		}
		return nil, fmt.Errorf("%s", detail)
	}
	return res.Result.Value, nil
}

// EvalString é Eval desserializando o valor em string.
func EvalString(ctx context.Context, c *cdp.Client, session, expr string) (string, error) {
	raw, err := Eval(ctx, c, session, expr)
	if err != nil {
		return "", err
	}
	var s string
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}
