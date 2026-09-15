// Screen reading: the accessibility tree becomes readable lines, with a stable
// `ref` to act on. It walks, decides what is noise and what is a target.
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
	// Page is the scroll state of the document, and ScrollAreas are the areas
	// that scroll inside it. They come from the DOM: the accessibility tree does
	// not carry scrolling.
	Page             *ScrollArea
	ScrollAreas      []ScrollArea
	ScrollAreasTotal int
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
}

// noiseNameRe recognizes the "page chrome": skip-navigation blocks, a WAI-ARIA
// pattern present on practically every site and useless to whoever acts by ref.
// It is not a hardcoded site name — it is the accessibility pattern.

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

	snap := buildText(nodes, opts)
	meta := readPageMeta(ctx, client, session)
	snap.Title, snap.URL = meta.Title, meta.URL
	snap.Page, snap.ScrollAreas, snap.ScrollAreasTotal = meta.Page, meta.ScrollAreas, meta.Total

	// Targets that the tree does not mark (a div with a click handler) enter as
	// a section at the end: without them, the agent has to guess a selector for
	// half the buttons of a real app.
	clickables, total := readClickables(ctx, client, session)
	if section := clickablesSection(clickables, total); section != "" {
		snap.Text += "\n" + section
		snap.Count += strings.Count(section, "\n") + 1
	}
	return snap, nil
}

// buildText is the pure part of the reading: it turns the raw accessibility tree
// into lines and refs, without touching the CDP. It is kept separate from
// TakeSnapshot because that is where all the noise cut lives — the most fragile
// logic in the project — and it is what can be tested with a fixture.

func buildText(nodes []axNode, opts SnapshotOptions) *Snapshot {
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

	// The root is not a line: its children start at depth 0, and go through the
	// same identical-sibling cut as the rest of the tree.
	b.walkChildren(root.NodeID, "", 0, "")

	return &Snapshot{
		Text:      strings.Join(b.out, "\n"),
		Refs:      b.refs,
		Count:     len(b.out),
		Truncated: b.truncated,
	}
}
