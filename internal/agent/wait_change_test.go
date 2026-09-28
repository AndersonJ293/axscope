package agent

import (
	"strings"
	"testing"

	"github.com/AndersonJ293/axscope/internal/protocol"
)

// A spinner churns class and style forever; observing them would keep a region
// from ever settling, so the filter names only attributes that mean content.
func TestObserveIgnoresAnimationAttributes(t *testing.T) {
	i := strings.Index(observeJS, "attributeFilter")
	if i < 0 {
		t.Fatal("the observer must filter attributes")
	}
	filter := observeJS[i : i+strings.Index(observeJS[i:], "]")]
	for _, noisy := range []string{"'class'", "'style'"} {
		if strings.Contains(filter, noisy) {
			t.Errorf("attributeFilter watches %s: %s", noisy, filter)
		}
	}
	for _, want := range []string{"aria-busy", "hidden", "aria-expanded"} {
		if !strings.Contains(filter, want) {
			t.Errorf("attributeFilter misses %s", want)
		}
	}
}

func TestFirstN(t *testing.T) {
	if got := firstN([]string{"a", "b", "c", "d"}, 2); strings.Join(got, ",") != "a,b,+2 more" {
		t.Errorf("firstN = %v", got)
	}
	if got := firstN([]string{"a"}, 2); len(got) != 1 {
		t.Errorf("firstN = %v", got)
	}
}

// wait --change counts from the start of the command before it; inside a batch
// that is the previous step, not the batch itself.
func TestPrevStartIsThePreviousStep(t *testing.T) {
	a := &Agent{}
	a.dispatch(t.Context(), protocol.Request{Cmd: "batch", Args: map[string]any{"steps": []any{"ping", "ping"}}})
	a.mu.Lock()
	prev, cur := a.prevStart, a.curStart
	a.mu.Unlock()
	if prev.IsZero() || cur.Before(prev) {
		t.Fatalf("prev=%v cur=%v", prev, cur)
	}
	// Two steps ran, so prev is the first step's start, which the batch (not a
	// command of its own) did not overwrite.
	before := cur
	a.dispatch(t.Context(), protocol.Request{Cmd: "ping"})
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.prevStart.Equal(before) {
		t.Errorf("prevStart = %v, want the last step's start %v", a.prevStart, before)
	}
}
