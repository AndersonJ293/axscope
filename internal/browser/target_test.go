package browser

import (
	"strings"
	"testing"

	"github.com/AndersonJ293/axscope/internal/dom"
)

// Regression (mission 13 of the lab): `pos=x,y` resolved the element under the
// point and then acted on its **center**. Over an iframe that is the iframe's
// center — dozens of pixels from the requested button — and the click hit
// nothing.
func TestTargetByPositionActsAtRequestedPoint(t *testing.T) {
	target := &Target{
		Rect:  dom.Rect{X: 279, Y: 428, Width: 628, Height: 170},
		Point: &Point{X: 385, Y: 527},
	}
	x, y := target.actionPoint()
	if x != 385 || y != 527 {
		t.Errorf("acted at %v,%v — it should act at the requested point, not at the element's center (%v,%v)",
			x, y, target.Rect.X+target.Rect.Width/2, target.Rect.Y+target.Rect.Height/2)
	}

	// Without a position, the element's center still holds.
	only := &Target{Rect: dom.Rect{X: 0, Y: 0, Width: 100, Height: 50}}
	if x, y := only.actionPoint(); x != 50 || y != 25 {
		t.Errorf("without pos= the center became %v,%v", x, y)
	}
}

// Regression (mission 12 of the lab): the accessibility tree **flattens** shadow
// DOM, so the reading shows the button that is inside a shadow root — with name
// and ref, and the ref even reaches it. Aiming by DOM walked only in the light
// document: the reading showed it and the `text=` did not find it.
func TestTargetExpressionsCrossShadowRoot(t *testing.T) {
	if expr := textExpression("Button in Shadow DOM"); !strings.Contains(expr, "shadowRoot") {
		t.Error("textExpression does not cross shadow root — the reading shows what is inside and the aim does not reach it")
	}

	// In css= the order matters: the light document first, because that is the
	// selector's semantics; the shadow is a way out, not a preference. This way
	// a page that always worked does not change its target.
	expr := cssExpression("#shadowButton")
	if !strings.Contains(expr, "document.querySelector") {
		t.Error("cssExpression does not try the light document")
	}
	if !strings.Contains(expr, "|| underShadow(") {
		t.Error("cssExpression does not fall back to the shadow when the light one finds nothing")
	}
}

// Regression (lab v2): a text target with no visible area yielded only "no
// visible area" — and whoever reads was left not knowing that the cause was the
// menu being closed, nor how many candidates existed. Six "Hide post" matched
// and none was in view.
func TestHiddenTextMessage(t *testing.T) {
	got := hiddenTextMessage("Hide post", 6)
	for _, wanted := range []string{"6", `"Hide post"`, "hidden", "open what reveals them"} {
		if !strings.Contains(got, wanted) {
			t.Errorf("message %q does not say %q", got, wanted)
		}
	}

	// The preference for visible must be in the search, and after the exact
	// name.
	expr := textExpression("Hide post")
	if !strings.Contains(expr, "hidden(el)") {
		t.Error("textExpression does not prefer a candidate in view")
	}
	if strings.Index(expr, "exact ? 0 : 1, hidden(el)") < 0 {
		t.Error("the order changed: exact must come before in view")
	}

	// requestedText only recognizes the text= form.
	if requestedText("css=#x") != "" || requestedText("e12") != "" {
		t.Error("requestedText is only valid for text=")
	}
	if requestedText("text= Waiting ") != "Waiting" {
		t.Errorf("requestedText = %q", requestedText("text= Waiting "))
	}
}
