package agent

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// find resolves a css=/text= target the same way click does and reports the ref
// the last snap gave it, without acting. `--all` lists every ref in the reading
// that matches instead of the single best one.
func (a *Agent) find(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	spec := req.String("target")
	if spec == "" {
		return protocol.Fail(fmt.Errorf("usage: axscope find <css=|text=> [--all]"))
	}
	refs := a.currentRefs()
	if len(refs) == 0 {
		return protocol.Fail(fmt.Errorf("nothing to match against: run `snap` first (refs are per reading)"))
	}
	if req.Bool("all", false) {
		return a.findAll(ctx, sess, spec, refs)
	}
	t, sid, err := a.resolve(ctx, sess, spec)
	if err != nil {
		return protocol.Fail(err)
	}
	backend, selector := browser.DescribeNode(ctx, a.client(), sid, t.ObjectID)
	if t.BackendNodeID != 0 {
		backend = t.BackendNodeID
	}
	line := fmt.Sprintf("target: %s\nselector: %s", spec, selector)
	ref := refForBackend(refs, backend)
	if ref == "" {
		return ok(line + "\nref: (none — the target is not one of the last snap's refs)")
	}
	return ok(line + "\nref: " + ref)
}

// findAll lists the refs of the current reading whose node matches the target. A
// ref is already a single target, so --all only takes css=/text=.
func (a *Agent) findAll(ctx context.Context, sess *browser.Session, spec string, refs map[string]int) protocol.Response {
	if !strings.HasPrefix(spec, "css=") && !strings.HasPrefix(spec, "text=") {
		return protocol.Fail(fmt.Errorf("find --all needs css= or text= (a ref is already one target)"))
	}
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	keys := make([]string, 0, len(refs))
	for ref := range refs {
		keys = append(keys, ref)
	}
	sort.Slice(keys, func(i, j int) bool { return refIndex(keys[i]) < refIndex(keys[j]) })
	var b strings.Builder
	found := 0
	for _, ref := range keys {
		objectID, err := browser.ResolveBackend(ctx, a.client(), sid, refs[ref])
		if err != nil || objectID == "" {
			continue
		}
		selector := browser.SelectorIfMatches(ctx, a.client(), sid, objectID, spec)
		if selector == "" {
			continue
		}
		fmt.Fprintf(&b, "%s  %s\n", ref, selector)
		found++
	}
	if found == 0 {
		return ok(fmt.Sprintf("no ref in the last snap matches %s", spec))
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}

// refIndex orders refs numerically ("e2" before "e10"), ignoring the generation.
func refIndex(ref string) int {
	base, _, _ := refGen(ref)
	n, _ := strconv.Atoi(strings.TrimPrefix(base, "e"))
	return n
}
