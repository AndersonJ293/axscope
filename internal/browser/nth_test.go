package browser

import (
	"strings"
	"testing"
)

func TestSplitNth(t *testing.T) {
	cases := []struct {
		spec, base string
		n          int
		bad        bool
	}{
		{"text=Save", "text=Save", -1, false},
		{"text=Save >> nth=1", "text=Save", 1, false},
		{"css=.card button>>nth=0", "css=.card button", 0, false},
		{"text=Save >> nth=-1", "", 0, true},
		{"text=Save >> visible=true", "", 0, true},
		{"text=Save >> nth=x", "", 0, true},
	}
	for _, c := range cases {
		base, n, err := splitNth(c.spec)
		if c.bad {
			if err == nil {
				t.Errorf("%q: accepted", c.spec)
			}
			continue
		}
		if err != nil || base != c.base || n != c.n {
			t.Errorf("%q = (%q, %d, %v), want (%q, %d)", c.spec, base, n, err, c.base, c.n)
		}
	}
}

// The note names the count and the exact next step, and stays quiet when the
// target was unambiguous.
func TestAmbiguityNote(t *testing.T) {
	if ambiguityNote("text=Save", 1) != "" || ambiguityNote("text=Save", 0) != "" {
		t.Error("one match is not ambiguous")
	}
	got := ambiguityNote("text=Save", 5)
	for _, want := range []string{"5 elements", "acted on the first", "`text=Save >> nth=1`", "0-based", "ref"} {
		if !strings.Contains(got, want) {
			t.Errorf("note lacks %q: %s", want, got)
		}
	}
}

// The tie group and the default search must agree on what a match is, so both
// are built from the same candidate and text helpers.
func TestTextGroupSharesTheSearchHelpers(t *testing.T) {
	expr := textGroupExpression(`Say "hi"`)
	for _, want := range []string{"underShadow", "const text =", "covered", "distinct(tied)", `"Say \"hi\""`} {
		if !strings.Contains(expr, want) {
			t.Errorf("group expression lacks %q", want)
		}
	}
}
