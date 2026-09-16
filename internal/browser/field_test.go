package browser

import (
	"strings"
	"testing"
)

// `fill` on a `<select>` or a `<label>` would answer ok and write nothing; the
// refusal must state the command that does what was wanted.
func TestClassifyField(t *testing.T) {
	cases := []struct {
		name        string
		field       fieldDescriptor
		accepts     bool
		mustContain string
	}{
		{"text input", fieldDescriptor{Tag: "INPUT", Type: "text"}, true, ""},
		{"input without type", fieldDescriptor{Tag: "INPUT"}, true, ""},
		{"number input", fieldDescriptor{Tag: "INPUT", Type: "number"}, true, ""},
		{"textarea", fieldDescriptor{Tag: "TEXTAREA"}, true, ""},
		{"contenteditable", fieldDescriptor{Tag: "DIV", Editable: true}, true, ""},
		{"role textbox", fieldDescriptor{Tag: "DIV", Role: "textbox"}, true, ""},
		{"select", fieldDescriptor{Tag: "SELECT"}, false, "axscope select"},
		{"checkbox", fieldDescriptor{Tag: "INPUT", Type: "checkbox"}, false, "axscope check"},
		{"radio", fieldDescriptor{Tag: "INPUT", Type: "radio"}, false, "axscope check"},
		{"file", fieldDescriptor{Tag: "INPUT", Type: "file"}, false, "axscope upload"},
		{"button", fieldDescriptor{Tag: "INPUT", Type: "submit"}, false, "axscope click"},
		{"label", fieldDescriptor{Tag: "LABEL"}, false, "not a text field"},
		{"div", fieldDescriptor{Tag: "DIV"}, false, "not a text field"},
	}
	for _, c := range cases {
		accepts, reason := classifyField(c.field)
		if accepts != c.accepts {
			t.Errorf("%s: accepts = %v, expected %v (reason %q)", c.name, accepts, c.accepts, reason)
		}
		if !accepts && !strings.Contains(reason, c.mustContain) {
			t.Errorf("%s: reason %q does not mention %q", c.name, reason, c.mustContain)
		}
	}
}

// The CDP only inserts with `text` in keyDown; without it Enter fires the handler
// and does not break the line.
func TestKeyText(t *testing.T) {
	if got := keyText("Enter"); got != "\r" {
		t.Errorf("Enter must insert the line break, and it inserts %q", got)
	}
	if got := keyText("Tab"); got != "\t" {
		t.Errorf("Tab must insert the tab, and it inserts %q", got)
	}
	for _, key := range []string{"Escape", "Backspace", "ArrowDown", "a"} {
		if got := keyText(key); got != "" {
			t.Errorf("%q inserts nothing, but it inserts %q", key, got)
		}
	}
}

// Typing a line break must become the Enter key: text with `\n` does not enter
// the field.
func TestKeyForAndLineBreaks(t *testing.T) {
	for _, r := range []rune{'\n', '\r'} {
		if key := keyFor(r); key != "Enter" {
			t.Errorf("line break (%q) must become Enter, it became %q", r, key)
		}
	}
	for _, r := range []rune{'a', 'ç', ' ', '\t'} {
		if key := keyFor(r); key != "" {
			t.Errorf("%q is not a named key, it became %q", r, key)
		}
	}

	if got := normalizeNewlines("a\r\nb\rc"); got != "a\nb\rc" {
		t.Errorf("normalizeNewlines = %q", got)
	}
}

// A mask changes the value on purpose (the phone becomes "(77) 9 9999-1111"), so
// a difference is a sign of nothing. Staying empty is.
func TestFillWarning(t *testing.T) {
	if got := fillWarning("(77) 99999-1111", "(77) 9 9999-1111"); got != "" {
		t.Errorf("a mask is not a failure: %q", got)
	}
	if got := fillWarning("text", "   "); got == "" {
		t.Error("an empty field after filling needs to warn")
	}
	if got := fillWarning("", ""); got != "" {
		t.Errorf("with no text asked there is nothing to warn about: %q", got)
	}
}
