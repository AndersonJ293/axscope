package browser

import (
	"context"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// offscreenNodes returns the DOM nodes (backend ids) whose box lies wholly
// outside the window, in one DOMSnapshot call. A node with no box (display:
// contents, a node the snapshot does not lay out) is not in the set: the
// reading walks into it and decides by its children. Frames are other
// documents and stay in, so an iframe's content is not dropped by mistake.
func offscreenNodes(ctx context.Context, client *cdp.Client, session string) (map[int]bool, error) {
	var metrics struct {
		CSSVisualViewport struct {
			PageX        float64 `json:"pageX"`
			PageY        float64 `json:"pageY"`
			ClientWidth  float64 `json:"clientWidth"`
			ClientHeight float64 `json:"clientHeight"`
		} `json:"cssVisualViewport"`
	}
	if err := client.SendJSON(ctx, "Page.getLayoutMetrics", map[string]any{}, session, &metrics); err != nil {
		return nil, err
	}
	var snap struct {
		Documents []struct {
			Nodes struct {
				BackendNodeID []int `json:"backendNodeId"`
			} `json:"nodes"`
			Layout struct {
				NodeIndex []int       `json:"nodeIndex"`
				Bounds    [][]float64 `json:"bounds"`
			} `json:"layout"`
		} `json:"documents"`
	}
	if err := client.SendJSON(ctx, "DOMSnapshot.captureSnapshot",
		map[string]any{"computedStyles": []string{}}, session, &snap); err != nil {
		return nil, err
	}
	if len(snap.Documents) == 0 {
		return map[int]bool{}, nil
	}
	v := metrics.CSSVisualViewport
	win := rect{v.PageX, v.PageY, v.ClientWidth, v.ClientHeight}
	doc := snap.Documents[0]
	out := map[int]bool{}
	for i, idx := range doc.Layout.NodeIndex {
		if i >= len(doc.Layout.Bounds) || idx >= len(doc.Nodes.BackendNodeID) {
			continue
		}
		b := doc.Layout.Bounds[i]
		if len(b) < 4 {
			continue
		}
		if !win.intersects(rect{b[0], b[1], b[2], b[3]}) {
			out[doc.Nodes.BackendNodeID[idx]] = true
		}
	}
	return out, nil
}

type rect struct{ x, y, w, h float64 }

// intersects is true when the boxes share area; an empty box at a point inside
// the window counts, since a zero-size wrapper can still hold visible content.
func (r rect) intersects(o rect) bool {
	return o.x <= r.x+r.w && o.x+o.w >= r.x && o.y <= r.y+r.h && o.y+o.h >= r.y
}
