package agent

import (
	"encoding/json"
	"testing"
)

// eval used to print CDP's result.value verbatim, so a JavaScript string came
// back double-serialized (quotes and escaped newlines). prettyValue presents it.
func TestPrettyValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"string loses the JSON quotes", `"hello"`, "hello"},
		{"escaped newline is decoded", `"a\nb"`, "a\nb"},
		{"escaped quote is decoded", `"say \"hi\""`, `say "hi"`},
		{"object is indented", `{"a":1}`, "{\n  \"a\": 1\n}"},
		{"array is indented", `[1,2]`, "[\n  1,\n  2\n]"},
		{"number keeps the exact text", `9007199254740993`, "9007199254740993"},
		{"bool passes through", `true`, "true"},
		{"null passes through", `null`, "null"},
		{"empty is undefined", ``, "undefined"},
	}
	for _, c := range cases {
		if got := prettyValue(json.RawMessage(c.in)); got != c.want {
			t.Errorf("%s: prettyValue(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// --raw keeps the exact CDP value, including the previous "undefined" fallback.
func TestRawValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"hello"`, `"hello"`},
		{`{"a":1}`, `{"a":1}`},
		{`42`, `42`},
		{``, "undefined"},
	}
	for _, c := range cases {
		if got := rawValue(json.RawMessage(c.in)); got != c.want {
			t.Errorf("rawValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
