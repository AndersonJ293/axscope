// Drag: the HTML5 and pointer families, the detection of which to use and the
// check that something actually changed.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

// DragOptions tunes the drag.
type DragOptions struct {
	// DropAt says where to drop onto the target: "" (center), "top" (25%) or
	// "bottom" (75%). It matters when the target decides before/after by position.
	DropAt string
	// Type forces the drag family: "html5" or "pointer". Empty detects.
	Type string
	// Steps is how many mouse steps along the pointer drag path.
	Steps int
}

// toRow swaps the target for the nearest list or table item, which is what
// defines the drop point for libraries that insert at the row under the pointer.
func toRow(ctx context.Context, client *cdp.Client, session string, t *Target) {
	if t == nil || t.ObjectID == "" {
		return
	}
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function () {
			return this.closest ? (this.closest('li,tr,[role="listitem"],[role="row"]') || this) : this;
		}`,
		"returnByValue": false,
	}, session)
	if err != nil {
		return
	}
	var res struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.ObjectID == "" || res.Result.ObjectID == t.ObjectID {
		return
	}
	rect, err := dom.BoxOf(ctx, client, session, res.Result.ObjectID)
	if err != nil {
		return
	}
	t.ObjectID = res.Result.ObjectID
	t.Rect = rect
}

// signatureOf summarizes where the element is (index among its siblings +
// position), to know whether the drag actually changed anything.
func signatureOf(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (!this || !this.parentElement) return '';
			const siblings = [...this.parentElement.children];
			const r = this.getBoundingClientRect();
			return siblings.indexOf(this) + '@' + Math.round(r.left) + ',' + Math.round(r.top) + '/' + siblings.length;
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return ""
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.Result.Value
}

// showCursor draws the cursor at the point; both calls are best effort.
func showCursor(ctx context.Context, client *cdp.Client, session string, p Presenter, rect *dom.Rect, x, y float64) {
	_ = p.Spotlight(ctx, client, session, rect)
	_ = p.MoveCursor(ctx, client, session, x, y)
}

// Drag drags `from` to `to`, detecting the family: HTML5 (built on the page with
// a real DataTransfer) or pointer (a real mouse). `type` forces detection.
func Drag(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) (string, bool, error) {
	// The row, not the text: it is what defines the drop point.
	toRow(ctx, client, session, from)
	toRow(ctx, client, session, to)

	// Stores where the origin was, to say whether something changed: a gesture
	// that does not take usually ends in a click, with no warning at all.
	before := signatureOf(ctx, client, session, from.ObjectID)

	kind := opts.Type
	if kind == "" {
		kind = dragKind(ctx, client, session, from.ObjectID)
	}
	var err error
	if kind == "pointer" {
		err = dragPointer(ctx, client, session, from, to, opts, p)
	} else {
		kind = "html5"
		err = dragHTML5(ctx, client, session, from, to, opts, p)
	}
	if err != nil {
		return kind, false, err
	}

	changed := true
	if before != "" {
		if after := signatureOf(ctx, client, session, from.ObjectID); after != "" {
			changed = before != after
		}
	}
	return kind, changed, nil
}

// dragKind reports "html5" or "pointer", keyed on the explicit
// `draggable="true"` attribute: the `draggable` property alone is true for image
// and link by default and would mark any click on them as HTML5.
func dragKind(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			return this.closest && this.closest('[draggable="true"]') ? 'html5' : 'pointer';
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return "html5"
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) == nil && res.Result.Value == "pointer" {
		return "pointer"
	}
	return "html5"
}

// dragHTML5 builds the drag on the page with a real DataTransfer, since an
// injected mouse does not create a dragstart.
func dragHTML5(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) error {
	fx, fy := from.actionPoint()
	tx, ty := dropPoint(to, opts.DropAt)

	// The cursor travels to the destination: whoever watches needs to see the
	// drag happen.
	showCursor(ctx, client, session, p, &from.Rect, fx, fy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	showCursor(ctx, client, session, p, &to.Rect, tx, ty)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}

	where := opts.DropAt
	if where == "" {
		where = "center"
	}
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            from.ObjectID,
		"functionDeclaration": dragScript,
		"arguments": []any{
			map[string]any{"objectId": to.ObjectID},
			map[string]any{"value": where},
		},
		"returnByValue": true,
	}, session)
	if err != nil {
		return err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		return fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	if res.Result.Value != "ok" {
		return fmt.Errorf("drag did not mount: %s", res.Result.Value)
	}
	time.Sleep(40 * time.Millisecond)
	return nil
}

// dragPointer drags with a real mouse (pointerdown/move/up). A page that ignores
// the gesture leaves a click behind, so it is used only when the origin is not
// `draggable`.
func dragPointer(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) error {
	if opts.Steps <= 0 {
		opts.Steps = 16
	}
	fx, fy := from.actionPoint()
	tx, ty := dropPoint(to, opts.DropAt)

	showCursor(ctx, client, session, p, &from.Rect, fx, fy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent",
		map[string]any{"type": "mouseMoved", "x": fx, "y": fy}, session); err != nil {
		return err
	}
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mousePressed", "x": fx, "y": fy,
		"button": "left", "buttons": 1, "clickCount": 1,
	}, session); err != nil {
		return err
	}
	// A pause before moving: a pointer library usually arms the gesture on
	// pointerdown and only starts tracking the movement on the next frame.
	time.Sleep(40 * time.Millisecond)

	for i := 1; i <= opts.Steps; i++ {
		x := fx + (tx-fx)*float64(i)/float64(opts.Steps)
		y := fy + (ty-fy)*float64(i)/float64(opts.Steps)
		if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type": "mouseMoved", "x": x, "y": y, "buttons": 1,
		}, session); err != nil {
			return err
		}
		time.Sleep(14 * time.Millisecond)
	}

	showCursor(ctx, client, session, p, &to.Rect, tx, ty)
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseReleased", "x": tx, "y": ty,
		"button": "left", "buttons": 0, "clickCount": 1,
	}, session); err != nil {
		return err
	}
	time.Sleep(60 * time.Millisecond)
	return nil
}

// dragScript emits the drag sequence over the real elements.
const dragScript = `function (target, where) {
	const source = this;
	if (!source || !target) return 'no source or destination';
	const point = (el) => {
		const b = el.getBoundingClientRect();
		let y = b.top + b.height / 2;
		if (where === 'top') y = b.top + b.height * 0.25;
		else if (where === 'bottom') y = b.top + b.height * 0.75;
		return { x: b.left + b.width / 2, y: y };
	};
	const ps = point(source);
	const pt = point(target);
	const dt = new DataTransfer();
	const fire = (type, el, p) => el.dispatchEvent(new DragEvent(type, {
		bubbles: true, cancelable: true, composed: true, dataTransfer: dt,
		clientX: p.x, clientY: p.y, screenX: p.x, screenY: p.y,
	}));
	fire('dragstart', source, ps);
	const over = document.elementFromPoint(pt.x, pt.y) || target;
	fire('dragenter', over, pt);
	fire('dragover', over, pt);
	fire('drop', over, pt);
	fire('dragend', source, pt);
	return 'ok';
}`

// dropPoint returns where to drop onto the target.
func dropPoint(t *Target, at string) (float64, float64) {
	switch at {
	case "top":
		return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height*0.25
	case "bottom":
		return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height*0.75
	default:
		return t.actionPoint()
	}
}
