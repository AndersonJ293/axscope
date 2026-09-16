package agent

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/AndersonJ293/axscope/internal/browser"
)

func TestRefGen(t *testing.T) {
	cases := []struct {
		spec string
		ref  string
		gen  int
		ok   bool
	}{
		{"e12#7", "e12", 7, true},
		{"e12", "e12", 0, false},
		{"#7", "#7", 0, false},
		{"e12#", "e12#", 0, false},
		{"e#a", "e#a", 0, false},
		// A CSS selector with "#" is not a ref with generation.
		{"css=#id", "css=#id", 0, false},
	}
	for _, c := range cases {
		ref, gen, ok := refGen(c.spec)
		if ref != c.ref || gen != c.gen || ok != c.ok {
			t.Errorf("refGen(%q) = (%q, %d, %v), want (%q, %d, %v)",
				c.spec, ref, gen, ok, c.ref, c.gen, c.ok)
		}
	}
}

func TestQualifyRef(t *testing.T) {
	a := &Agent{snapGen: 7, refs: map[string]int{"e12#7": 5, "e46#3": 9}}
	cases := []struct{ in, want string }{
		// A plain ref is the current reading, as the docs show.
		{"e12", "e12#7"},
		// An explicit generation is kept for resolve to validate.
		{"e12#7", "e12#7"},
		{"e12#3", "e12#3"},
		// A ref that is not in the current reading is left as-is, so the error
		// still names what the caller typed.
		{"e99", "e99"},
		{"e46", "e46"}, // e46#3 exists, but not in the current reading
		// The other target forms pass through untouched.
		{"css=#id", "css=#id"},
		{"text=Sign in", "text=Sign in"},
		{"pos=10,20", "pos=10,20"},
	}
	for _, c := range cases {
		if got := a.qualifyRef(c.in); got != c.want {
			t.Errorf("qualifyRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Before the first read there is no generation to complete with.
	empty := &Agent{}
	if got := empty.qualifyRef("e1"); got != "e1" {
		t.Errorf("qualifyRef without a read = %q, want %q", got, "e1")
	}
}

func TestSplitTokens(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		tokens  []string
		wantErr bool
	}{
		{"simple", "wait pronto", []string{"wait", "pronto"}, false},
		{"double quotes", `open "http://a b"`, []string{"open", "http://a b"}, false},
		{"single quotes", "click 'e1'", []string{"click", "e1"}, false},
		{"empty string between quotes", `fill e1 ""`, []string{"fill", "e1", ""}, false},
		{"multiple spaces", "  wait   pronto  ", []string{"wait", "pronto"}, false},
		{"unterminated quotes", `open "http://a`, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := splitTokens(c.line)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.tokens) {
				t.Errorf("tokens = %#v, want %#v", got, c.tokens)
			}
		})
	}
}

// The wait scan must cross shadow roots and same-origin iframes: the read shows
// their content, so the wait has to see it too. The test pins both boundaries.
func TestTextLocator(t *testing.T) {
	expr := textLocator("Iframe zone")
	for _, want := range []string{"shadowRoot", "contentDocument", "IFRAME", "inside iframe"} {
		if !strings.Contains(expr, want) {
			t.Errorf("the wait scan does not cross %q", want)
		}
	}
	if !strings.Contains(expr, strconv.Quote("Iframe zone")) {
		t.Error("the sought text did not enter the expression")
	}
	if !strings.Contains(expr, "walk(this, false)") {
		t.Error("the scan must start at the object where it runs, so that within= limits the search")
	}
}

// Regression: a click that does not arrive used to answer `ok` like a working
// click while still possibly landing on the top layer. It is now refused with the
// reason and the next step.
func TestActionFailure(t *testing.T) {
	err := actionFailure("click", "text=Gostei 20", fmt.Errorf("the target is covered by div.modal-backdrop — to click the point anyway, use pos=x,y"))
	got := err.Error()
	for _, want := range []string{"click on text=Gostei 20", "covered by div.modal-backdrop", "pos=x,y"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q does not say %q", got, want)
		}
	}
}

// Regression: the accessibility tree does not carry scroll state, so the read
// header now reports where in a scrolling area the agent is.
func TestScrollLine(t *testing.T) {
	if got := scrollLine(&browser.Snapshot{}); got != "" {
		t.Errorf("with nothing scrolling the header should not change: %q", got)
	}

	snap := &browser.Snapshot{
		Page:             &browser.ScrollArea{Name: "page", Pos: 2275, Max: 2830},
		ScrollAreas:      []browser.ScrollArea{{Name: "#virtual", Pos: 32680, Max: 41672}},
		ScrollAreasTotal: 2,
	}
	got := scrollLine(snap)
	for _, want := range []string{"page 2275/2830", "#virtual 32680/41672"} {
		if !strings.Contains(got, want) {
			t.Errorf("line %q does not say %q", got, want)
		}
	}

	// Many areas: summarize instead of inventorying.
	many := &browser.Snapshot{ScrollAreasTotal: 9}
	for i := 0; i < 8; i++ {
		many.ScrollAreas = append(many.ScrollAreas, browser.ScrollArea{Name: "div", Pos: i, Max: 100})
	}
	if got := scrollLine(many); !strings.Contains(got, "(+4)") {
		t.Errorf("expected the summary of the remaining areas: %q", got)
	}
}

func TestSqueeze(t *testing.T) {
	cases := []struct{ input, want string }{
		{"a\n\n\nb", "a\n\nb"},
		{"a  \nb\t\n", "a\nb"},
		{"\n\n  a  \n\n", "a"},
		{"a\nb\nc", "a\nb\nc"},
	}
	for _, c := range cases {
		if got := squeeze(c.input); got != c.want {
			t.Errorf("squeeze(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
