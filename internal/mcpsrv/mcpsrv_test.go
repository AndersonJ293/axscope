package mcpsrv

import "testing"

// Regression (lab v3): the refusal messages from `fill` point to `axscope
// select`, `axscope check` and `axscope upload` — and none of the three was in
// the exposed catalog. The agent was told to use what it could not call, and had
// to resort to `eval` to choose an option of `<select>`.
//
// The test pins the contract: the command the tool tells you to use has to be
// reachable.
func TestCatalogCoversTheCommandsTheMessagesCite(t *testing.T) {
	required := []string{"select", "check", "uncheck", "type", "upload"}
	exposed := map[string]bool{}
	for _, td := range tools() {
		exposed[td.Name] = true
	}
	for _, cmd := range required {
		if !exposed[cmd] {
			t.Errorf("%q is not exposed in the catalog, but the refusal messages tell you to use it", cmd)
		}
	}
}

// The catalog remains a lean set: if it grows by carelessness, this is where it
// is noticed (each schema costs context on every request).
func TestCatalogDoesNotGrowByCarelessness(t *testing.T) {
	if n := len(tools()); n > 30 {
		t.Errorf("the curated catalog has %d tools — above that the context cost stops paying off", n)
	}
}
