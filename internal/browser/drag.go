// Drag: the two families that coexist on the web (HTML5 and pointer), the
// detection of which to use and the check that something actually changed.
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
	// DropAt says where to drop onto the target: "" (center), "top" (25% from
	// the top) or "bottom" (75%). It matters when the target decides
	// before/after by position.
	DropAt string
	// Type forces the drag family: "html5" or "pointer". Empty detects.
	Type string
	// Steps is how many mouse steps along the pointer drag path.
	Steps int
}

// toRow swaps the target for the "row" that contains it — the nearest list or
// table item.
//
// Discovered in the LinkedIn reorder: aiming at the paragraph (≈20px, centered
// on the 48px row) or the whole row changes the drop point — and the library
// inserts at the index of the row under the pointer. Three attempts missed the
// position because of that; aiming at the row, it hit on the first try.
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

// Drag drags `from` to `to`.
//
// Two drag families coexist on the web and do not talk to each other:
//
//   - HTML5 (`draggable`, dragstart/dragover/drop): an injected mouse does NOT
//     start the gesture — Chrome only creates the dragstart from real user
//     input. Here the drag is built on the page, with a real DataTransfer.
//   - pointer (pointerdown/move/up moving the element, e.g. dnd-kit): it is the
//     opposite — what works is a real mouse, and a synthetic event is ignored.
//
// The origin with `draggable="true"` (on it or on an ancestor) says which family
// it is. `type=pointer|html5` forces it, if the detection misses.
func Drag(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) (string, bool, error) {
	// The row, not the text: it is what defines the drop point.
	toRow(ctx, client, session, from)
	toRow(ctx, client, session, to)

	// It stores where the origin was, to be able to say whether something
	// actually changed — a gesture that does not take usually ends in a click,
	// with no warning at all.
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

// dragKind says which family the drag is: "html5" or "pointer".
//
// The signal is the explicit `draggable="true"` attribute — the `draggable`
// property alone does not serve, because image and link are already draggable by
// default and would mark as HTML5 any click on them.
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

// dragHTML5 builds the drag on the page. It is the HTML5 family path, which
// ignores an injected mouse: we emit dragstart/dragenter/dragover/drop/dragend
// with a real DataTransfer, over the element under the drop point (the event
// bubbles, so whoever listens on the container also receives it).
func dragHTML5(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) error {
	fx, fy := from.actionPoint()
	tx, ty := dropPoint(to, opts.DropAt)

	// The cursor travels to the destination: whoever watches needs to see the
	// drag happen.
	_ = p.Spotlight(ctx, client, session, &from.Rect)
	_ = p.MoveCursor(ctx, client, session, fx, fy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	_ = p.Spotlight(ctx, client, session, &to.Rect)
	_ = p.MoveCursor(ctx, client, session, tx, ty)
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

// dragPointer drags with a real mouse: it is what the pointer family understands
// (pointerdown/move/up). If the page ignores the gesture, a click is left over —
// that is why this path is only used when the origin is NOT `draggable`.
func dragPointer(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) error {
	if opts.Steps <= 0 {
		opts.Steps = 16
	}
	fx, fy := from.actionPoint()
	tx, ty := dropPoint(to, opts.DropAt)

	_ = p.Spotlight(ctx, client, session, &from.Rect)
	_ = p.MoveCursor(ctx, client, session, fx, fy)
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

	_ = p.Spotlight(ctx, client, session, &to.Rect)
	_ = p.MoveCursor(ctx, client, session, tx, ty)
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
