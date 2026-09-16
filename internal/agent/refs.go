package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
)

func (a *Agent) setRefs(refs map[string]int, gen int) {
	a.mu.Lock()
	a.refs = refs
	a.snapGen = gen
	a.mu.Unlock()
}

// nextGen is the generation of the next read (the current one + 1).
func (a *Agent) nextGen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapGen + 1
}

// refGen separates the ref from the generation: "e12#7" -> ("e12", 7, true).
func refGen(spec string) (string, int, bool) {
	i := strings.LastIndex(spec, "#")
	if i <= 0 || i == len(spec)-1 {
		return spec, 0, false
	}
	n, err := strconv.Atoi(spec[i+1:])
	if err != nil {
		return spec, 0, false
	}
	return spec[:i], n, true
}

func (a *Agent) currentRefs() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refs
}

// refForBackend returns the ref the current reading gave a backend node id, or
// "" when the node is not one of its targets.
func refForBackend(refs map[string]int, backendID int) string {
	if backendID == 0 {
		return ""
	}
	for ref, id := range refs {
		if id == backendID {
			return ref
		}
	}
	return ""
}

// qualifyRef completes a plain ref with the current reading's generation, so
// `click e12` keeps working as the docs show: `snap` prints `e12#7`, but the
// caller does not have to repeat the suffix it just read. An explicit
// generation is left alone for resolve to validate, and anything that is not a
// bare ref (css=, text=, pos=) passes through untouched.
func (a *Agent) qualifyRef(spec string) string {
	if strings.HasPrefix(spec, "css=") || strings.HasPrefix(spec, "text=") || strings.HasPrefix(spec, "pos=") {
		return spec
	}
	if _, _, ok := refGen(spec); ok {
		return spec
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapGen <= 0 {
		return spec
	}
	full := spec + "#" + strconv.Itoa(a.snapGen)
	if _, ok := a.refs[full]; ok {
		return full
	}
	return spec
}

// resolve turns a target (ref/css/text/pos) into a Target and refuses a ref from
// an older read, which would otherwise point to whatever now occupies that position.
func (a *Agent) resolve(ctx context.Context, sess *browser.Session, target string) (*browser.Target, string, error) {
	sid, err := sess.ActiveSID()
	if err != nil {
		return nil, "", err
	}
	target = a.qualifyRef(target)
	if !strings.HasPrefix(target, "css=") && !strings.HasPrefix(target, "text=") {
		if _, gen, ok := refGen(target); ok {
			a.mu.Lock()
			current := a.snapGen
			a.mu.Unlock()
			if gen != current {
				return nil, sid, fmt.Errorf(
					"ref %q is from an old read (the current one is #%d) — run `snap` again", target, current)
			}
		}
	}
	t, err := browser.ResolveTarget(ctx, a.client(), sid, a.currentRefs(), target)
	if err != nil {
		return nil, sid, err
	}
	return t, sid, nil
}
