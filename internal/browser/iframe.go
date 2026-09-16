// Iframes in the reading: each frame's accessibility tree is grafted onto the
// iframe's node so the inner content appears. OOPIFs (different origin) stay out.
package browser

import (
	"context"
	"fmt"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// frameInfo is the frame as Page.getFrameTree describes it.
type frameInfo struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	ParentID string `json:"parentId"`
}

// frameTree is the recursive format of Page.getFrameTree.
type frameTree struct {
	Frame       frameInfo   `json:"frame"`
	ChildFrames []frameTree `json:"childFrames"`
}

// hasIframe says whether it is worth fetching the frames; without an iframe there
// is nothing to join and most pages have none.
func hasIframe(nodes []axNode) bool {
	for i := range nodes {
		if frameRoles[nodes[i].Role.str()] {
			return true
		}
	}
	return false
}

// joinFrames returns the main frame's tree with each child frame's hung on the
// corresponding iframe node. With no child frame, it returns the same thing it
// received.
func joinFrames(ctx context.Context, client *cdp.Client, session string, nodes []axNode) []axNode {
	frames := childFrames(ctx, client, session)
	if len(frames) == 0 {
		return nodes
	}
	out := append([]axNode(nil), nodes...)
	for i, f := range frames {
		owner, err := frameOwner(ctx, client, session, f.ID)
		if err != nil || owner == 0 {
			continue
		}
		parent := nodeByBackend(out, owner)
		if parent == "" {
			continue
		}
		var t struct {
			Nodes []axNode `json:"nodes"`
		}
		if err := client.SendJSON(ctx, "Accessibility.getFullAXTree",
			map[string]any{"frameId": f.ID}, session, &t); err != nil || len(t.Nodes) == 0 {
			continue
		}
		out = append(out, graftFrame(t.Nodes, fmt.Sprintf("f%d:", i), parent)...)
	}
	return out
}

// childFrames lists the non-main frames outermost to innermost; an inner frame
// only has somewhere to hang after the outer one has entered.
func childFrames(ctx context.Context, client *cdp.Client, session string) []frameInfo {
	var res struct {
		FrameTree frameTree `json:"frameTree"`
	}
	if err := client.SendJSON(ctx, "Page.getFrameTree", map[string]any{}, session, &res); err != nil {
		return nil
	}
	var out []frameInfo
	var visit func(a frameTree, root bool)
	visit = func(a frameTree, root bool) {
		if !root && a.Frame.ID != "" {
			out = append(out, a.Frame)
		}
		for _, c := range a.ChildFrames {
			visit(c, false)
		}
	}
	visit(res.FrameTree, true)
	return out
}

// frameOwner returns the backendNodeId of the element that hosts the frame.
func frameOwner(ctx context.Context, client *cdp.Client, session, frameID string) (int, error) {
	var res struct {
		BackendNodeID int `json:"backendNodeId"`
	}
	err := client.SendJSON(ctx, "DOM.getFrameOwner", map[string]any{"frameId": frameID}, session, &res)
	return res.BackendNodeID, err
}

// nodeByBackend finds, in the tree built so far, the node of the element.
func nodeByBackend(nodes []axNode, backend int) string {
	for i := range nodes {
		if nodes[i].BackendDOMNodeID == backend {
			return nodes[i].NodeID
		}
	}
	return ""
}

// graftFrame prepares a frame's tree to enter the main one: the frame root drops
// out and its children hang on the iframe's node. The id prefix is mandatory,
// since each tree numbers its nodes from its own root and the ids would collide.
func graftFrame(nodes []axNode, prefix, parent string) []axNode {
	root := ""
	for i := range nodes {
		if nodes[i].ParentID == "" {
			root = nodes[i].NodeID
			break
		}
	}
	out := make([]axNode, 0, len(nodes))
	for _, n := range nodes {
		if n.NodeID == root {
			continue
		}
		n.NodeID = prefix + n.NodeID
		switch n.ParentID {
		case "", root:
			n.ParentID = parent
		default:
			n.ParentID = prefix + n.ParentID
		}
		children := make([]string, len(n.ChildIDs))
		for j, c := range n.ChildIDs {
			children[j] = prefix + c
		}
		n.ChildIDs = children
		out = append(out, n)
	}
	return out
}
