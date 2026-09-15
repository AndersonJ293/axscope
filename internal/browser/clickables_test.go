package browser

import (
	"strings"
	"testing"
)

// The section must give the selector and the label, summarize what did not fit,
// and vanish when there is nothing — the reading cannot gain an empty block.
func TestClickablesSection(t *testing.T) {
	if got := clickablesSection(nil, 0); got != "" {
		t.Errorf("with no items the section should not exist: %q", got)
	}

	items := []Clickable{
		{Selector: `div.thread[data-id="t1"]`, Label: "Ana Souza — Hi Ana! I'm interested"},
		{Selector: `div.thread[data-id="t2"]`, Label: "Bruno Lima — That article about queues"},
	}
	got := clickablesSection(items, 2)
	for _, wanted := range []string{"clickables", `div.thread[data-id="t1"]`, "Ana Souza", `div.thread[data-id="t2"]`} {
		if !strings.Contains(got, wanted) {
			t.Errorf("section %q does not say %q", got, wanted)
		}
	}
	if strings.Contains(got, "(+") {
		t.Errorf("nothing was left out, it should not summarize: %q", got)
	}

	// Above the ceiling, the rest becomes a count.
	many := make([]Clickable, 0, maxClickables+4)
	for i := 0; i < maxClickables+4; i++ {
		many = append(many, Clickable{Selector: "div.item", Label: "item"})
	}
	got = clickablesSection(many, len(many))
	if !strings.Contains(got, "(+4)") {
		t.Errorf("expected the summary of what was left: %q", got)
	}
	if n := strings.Count(got, "\n  div.item"); n != maxClickables {
		t.Errorf("showed %d items, expected %d", n, maxClickables)
	}
}
