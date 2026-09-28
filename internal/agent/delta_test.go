package agent

import (
	"os"
	"strings"
	"testing"
)

// A line that kept its ref number reads the same across generations; a delta
// shows only the lines that changed, with one line of context.
func TestDeltaShowsOnlyWhatChanged(t *testing.T) {
	prev := []string{
		`- heading "Countries"`,
		`- textbox "Search" [ref=e1#3]`,
		`- listbox`,
		`  - option "Argentina" [ref=e2#3]`,
		`- button "Next" [ref=e3#3]`,
		`- link "Help" [ref=e4#3]`,
		`- link "About" [ref=e5#3]`,
	}
	cur := []string{
		`- heading "Countries"`,
		`- textbox "Search" [ref=e1#4]`,
		`- listbox`,
		`  - option "Brasil" [ref=e6#4]`,
		`- button "Next" [ref=e3#4]`,
		`- link "Help" [ref=e4#4]`,
		`- link "About" [ref=e5#4]`,
	}
	text, added, removed, fits := delta(prev, cur)
	if !fits || added != 1 || removed != 1 {
		t.Fatalf("added=%d removed=%d fits=%v\n%s", added, removed, fits, text)
	}
	for _, want := range []string{`+   - option "Brasil" [ref=e6#4]`, `-   - option "Argentina" [ref=e2#3]`, `  - listbox`, `  - button "Next" [ref=e3#4]`} {
		if !strings.Contains(text, want) {
			t.Errorf("delta lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "About") {
		t.Errorf("an unchanged line far from the change leaked:\n%s", text)
	}
}

func TestDeltaNoChangeAndMostlyChanged(t *testing.T) {
	same := []string{`- button "A" [ref=e1#1]`}
	if _, a, r, fits := delta(same, []string{`- button "A" [ref=e1#2]`}); !fits || a+r != 0 {
		t.Errorf("a new generation alone is not a change: +%d -%d", a, r)
	}
	if _, _, _, fits := delta([]string{"- a", "- b"}, []string{"- c", "- d"}); fits {
		t.Error("a page that changed entirely must fall back to the full reading")
	}
}

func TestRefNumbers(t *testing.T) {
	got := refNumbers(map[string]int{"e12#7": 400, "e3": 9})
	if got[400] != 12 || got[9] != 3 {
		t.Errorf("refNumbers = %v", got)
	}
}

// Past the cap the answer keeps the start, cut at a line, and says where the
// whole reading is and how to read less.
func TestCapReading(t *testing.T) {
	text := strings.Repeat("- link \"x\" [ref=e1#1]\n", 100)
	if got := capReading(text, 0); got != text {
		t.Error("cap 0 must not cut")
	}
	got := capReading(text, 200)
	if len(got) > 400 || !strings.Contains(got, "-- cut at") || !strings.Contains(got, "within=") {
		t.Fatalf("capped reading:\n%s", got)
	}
	path := got[strings.Index(got, "is in ")+6 : strings.Index(got, " — or")]
	data, err := os.ReadFile(path)
	if err != nil || string(data) != text {
		t.Errorf("the file does not hold the whole reading: %v", err)
	}
	_ = os.Remove(path)
}

func TestSnapCap(t *testing.T) {
	for env, want := range map[string]int{"": defaultSnapBytes, "5000": 5000, "0": 0, "-1": 0, "x": defaultSnapBytes} {
		t.Setenv("AXSCOPE_SNAP_MAX_BYTES", env)
		if got := snapCap(); got != want {
			t.Errorf("AXSCOPE_SNAP_MAX_BYTES=%q: %d, want %d", env, got, want)
		}
	}
}
