package agent

import (
	"strings"
	"testing"
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
