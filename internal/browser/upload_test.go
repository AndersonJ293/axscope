package browser

import (
	"strings"
	"testing"
)

// upload without a target must look inside open shadow roots: an app rendered in
// one (LinkedIn's `#interop-outlet`) has no `<input type=file>` in the light DOM,
// which is what made `upload` answer "could not find <input type=file>" there.
func TestFileInputSearchPiercesShadow(t *testing.T) {
	for _, want := range []string{"input[type=file]", "shadowRoot"} {
		if !strings.Contains(shadowFileInputScript, want) {
			t.Errorf("the file-input search does not use %q", want)
		}
	}
	if !strings.Contains(firstFileInputExpression, "see(document)") {
		t.Error("the file-input expression does not start from the document")
	}
}
