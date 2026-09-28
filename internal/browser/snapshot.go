// Screen reading: the accessibility tree becomes readable lines with a stable
// `ref` to act on.
package browser

import (
	"context"
	"fmt"
	"strings"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Snapshot is the screen read, with the ref map for the next step.
type Snapshot struct {
	Text      string
	Refs      map[string]int // "e12" -> backendNodeId
	Count     int
	Title     string
	URL       string
	Truncated bool
	// Page is the scroll state of the document and ScrollAreas the areas that
	// scroll inside it; both come from the DOM.
	Page             *ScrollArea
	ScrollAreas      []ScrollArea
	ScrollAreasTotal int
	// Offscreen counts the targets --viewport left out.
	Offscreen int
	// Scope names the part of the tree the reading starts from (empty = the
	// page); ScopeAuto says a modal chose it, not the agent.
	Scope     string
	ScopeAuto bool
}

// SnapshotOptions controls the size of the reading.

type SnapshotOptions struct {
	MaxNodes int
	// RefsOnly lists only the actionable targets, without loose text.
	RefsOnly bool
	// All turns off the page chrome cut (footer and shortcut links).
	All bool
	// Gen is the generation of the reading. It enters the ref (e12#7) so that a
	// ref from an old reading is refused instead of pointing at another element.
	Gen int
	// Scope is the DOM node (backend id) the reading starts from; 0 = the page.
	Scope int
	// ScopeLabel names a scope the tree has no node for (a plain div), which is
	// then read as the nodes inside it.
	ScopeLabel string
	scopeNodes map[string]bool
	// PrevRefs carries the previous reading's numbers (backend id -> N of eN),
	// so an element keeps its ref across readings of the same page: a delta
	// that does not repeat a line must not renumber it behind the agent's back.
	PrevRefs map[int]int
	// Viewport reads only what is inside the window.
	Viewport  bool
	offscreen map[int]bool
	// Page turns off the automatic scope to an open modal dialog.
	Page bool
	// MaxDepth cuts the reading at that nesting depth; each cut container keeps a
	// line with a ref and the count of targets inside. 0 = no cut.
	MaxDepth int
}

// noiseNameRe recognizes the "page chrome": skip-navigation blocks, a WAI-ARIA
// pattern present on almost every site and useless to whoever acts by ref.

type snapBuilder struct {
	nodes       map[string]*axNode
	children    map[string][]string
	refs        map[string]int
	out         []string
	consumed    map[string]bool
	targetCache map[string]bool
	nextRef     int
	max         int
	refsOnly    bool
	all         bool
	gen         int
	maxDepth    int
	offscreen   map[int]bool
	prev        map[int]int
	used        map[int]bool
	skipped     int
	truncated   bool
}

// TakeSnapshot reads the screen of the informed session (tab).

func TakeSnapshot(ctx context.Context, client *cdp.Client, session string, opts SnapshotOptions) (*Snapshot, error) {
	var tree struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := client.SendJSON(ctx, "Accessibility.getFullAXTree", map[string]any{}, session, &tree); err != nil {
		return nil, err
	}
	if len(tree.Nodes) == 0 {
		return nil, fmt.Errorf("empty accessibility tree")
	}

	// An iframe is another tree: the main frame's shows the iframe as a single
	// line. Without joining them, the reading does not say what is inside.
	nodes := tree.Nodes
	if hasIframe(nodes) {
		nodes = joinFrames(ctx, client, session, nodes)
	}

	// A plain container is not in the tree, but what it holds is: queryAXTree
	// lists the tree nodes inside a DOM node, and those are read as the scope.
	if opts.Scope > 0 && findBackend(nodes, opts.Scope) == nil {
		opts.scopeNodes = queryScope(ctx, client, session, opts.Scope)
	}
	if opts.Viewport {
		off, err := offscreenNodes(ctx, client, session)
		if err != nil {
			return nil, fmt.Errorf("--viewport: %w", err)
		}
		opts.offscreen = off
	}
	snap, err := buildText(nodes, opts)
	if err != nil {
		return nil, err
	}
	meta := readPageMeta(ctx, client, session)
	snap.Title, snap.URL = meta.Title, meta.URL
	snap.Page, snap.ScrollAreas, snap.ScrollAreasTotal = meta.Page, meta.ScrollAreas, meta.Total

	// A scoped reading is about one part of the page: the page-wide sweep of
	// unmarked targets would bring the rest of the page back in.
	if snap.Scope != "" {
		return snap, nil
	}

	// Targets the tree does not mark (a div with a click handler) enter as a
	// section at the end, or the agent must guess a selector for half the buttons.
	clickables, total := readClickables(ctx, client, session)
	if section := clickablesSection(clickables, total); section != "" {
		snap.Text += "\n" + section
		snap.Count += strings.Count(section, "\n") + 1
	}
	return snap, nil
}

// queryScope returns the ids of the tree nodes inside a DOM node.
func queryScope(ctx context.Context, client *cdp.Client, session string, backend int) map[string]bool {
	var res struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := client.SendJSON(ctx, "Accessibility.queryAXTree",
		map[string]any{"backendNodeId": backend}, session, &res); err != nil {
		return nil
	}
	ids := make(map[string]bool, len(res.Nodes))
	for _, n := range res.Nodes {
		ids[n.NodeID] = true
	}
	return ids
}

// buildText turns the raw accessibility tree into lines and refs without the
// CDP, kept separate because the noise cut — the most fragile logic here — is
// what can be tested with a fixture.
func buildText(nodes []axNode, opts SnapshotOptions) (*Snapshot, error) {
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 1500
	}
	b := &snapBuilder{
		nodes:       make(map[string]*axNode, len(nodes)),
		children:    make(map[string][]string),
		refs:        make(map[string]int),
		consumed:    make(map[string]bool),
		targetCache: make(map[string]bool),
		max:         opts.MaxNodes,
		refsOnly:    opts.RefsOnly,
		all:         opts.All,
		gen:         opts.Gen,
		maxDepth:    opts.MaxDepth,
		offscreen:   opts.offscreen,
		prev:        opts.PrevRefs,
		used:        map[int]bool{},
	}
	for _, n := range opts.PrevRefs {
		b.nextRef = max(b.nextRef, n)
	}
	var root *axNode
	for i := range nodes {
		n := &nodes[i]
		b.nodes[n.NodeID] = n
		if n.ParentID == "" {
			root = n
		}
	}
	for _, n := range nodes {
		if n.ParentID != "" {
			b.children[n.ParentID] = append(b.children[n.ParentID], n.NodeID)
		}
	}
	if root == nil {
		root = &nodes[0]
	}

	// A scope is read as a subtree whose top is a line of its own: the dialog
	// or the container the agent named is part of what it asked to see.
	var scope *axNode
	auto := false
	switch {
	case opts.Scope > 0:
		scope = findBackend(nodes, opts.Scope)
		if scope == nil && len(opts.scopeNodes) == 0 {
			return nil, fmt.Errorf("nothing readable inside the element (empty, hidden, or inert behind a modal)")
		}
	case !opts.Page:
		scope = findModal(nodes)
		auto = scope != nil
	}

	switch {
	case scope != nil:
		b.walk(scope.NodeID, 0, "")
	case len(opts.scopeNodes) > 0:
		// The container itself has no node: its topmost nodes inside are read
		// as siblings at depth 0, in tree order.
		for i := range nodes {
			id := nodes[i].NodeID
			if opts.scopeNodes[id] && !opts.scopeNodes[nodes[i].ParentID] {
				b.walk(id, 0, "")
			}
		}
	default:
		// The root is not a line: its children start at depth 0, and go through
		// the same identical-sibling cut as the rest of the tree.
		b.walkChildren(root.NodeID, "", 0, "")
	}

	snap := &Snapshot{
		Text:      strings.Join(b.out, "\n"),
		Refs:      b.refs,
		Count:     len(b.out),
		Truncated: b.truncated,
		ScopeAuto: auto,
		Offscreen: b.skipped,
	}
	switch {
	case scope != nil:
		snap.Scope = scopeLabel(scope)
		// A bare "generic" says nothing; the DOM's div.example does.
		if anonRoles[scope.Role.str()] && norm(scope.Name.str()) == "" && opts.ScopeLabel != "" {
			snap.Scope = opts.ScopeLabel
		}
	case len(opts.scopeNodes) > 0:
		snap.Scope = opts.ScopeLabel
		if snap.Scope == "" {
			snap.Scope = "element"
		}
	}
	return snap, nil
}
