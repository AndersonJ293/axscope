package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// viewport emulates a device viewport — a 360x800 phone, say — so a responsive
// layout can be tested without resizing the window, or resets it to the real one.
func (a *Agent) viewport(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	size := strings.TrimSpace(req.String("size"))
	if req.Bool("reset", false) || size == "reset" || size == "off" || size == "real" {
		if err := browser.ResetViewport(ctx, a.client(), sid); err != nil {
			return protocol.Fail(err)
		}
		return ok("ok: viewport reset to the real window")
	}
	if size == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope viewport <width>x<height> [scale=2] [mobile=1] | axscope viewport --reset"))
	}
	w, h, err := parseViewportSize(size)
	if err != nil {
		return protocol.Fail(err)
	}
	scale := 1.0
	if s := req.String("scale"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v <= 0 {
			return protocol.Fail(fmt.Errorf("scale must be a positive number"))
		}
		scale = v
	}
	mobile := argOn(req.String("mobile"))
	before := a.errCount(sess, sid)
	if err := browser.SetViewport(ctx, a.client(), sid, browser.Viewport{
		Width: w, Height: h, Scale: scale, Mobile: mobile,
	}); err != nil {
		return protocol.Fail(err)
	}
	label := fmt.Sprintf("viewport %dx%d", w, h)
	if scale != 1 {
		label += fmt.Sprintf(" scale=%g", scale)
	}
	if mobile {
		label += " mobile"
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}

// parseViewportSize reads "360x800" (x, X and * all separate the sides).
func parseViewportSize(size string) (int, int, error) {
	parts := strings.FieldsFunc(size, func(r rune) bool { return r == 'x' || r == 'X' || r == '*' })
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("size must be <width>x<height> (for example 360x800)")
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("size must be <width>x<height> with positive numbers (for example 360x800)")
	}
	return w, h, nil
}

// argOn reads a boolean-ish argument ("1", "true", "yes", "on").
func argOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
