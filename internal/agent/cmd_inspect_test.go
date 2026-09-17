package agent

import (
	"encoding/json"
	"testing"

	"github.com/AndersonJ293/axscope/internal/browser"
)

// status names where the session's CDP lives, so a QA agent can fall back to raw
// CDP without reconstructing it. Extension mode has no endpoint, and attach is
// marked so it is not confused with a browser axscope launched.
func TestCDPEndpoint(t *testing.T) {
	cases := []struct {
		name   string
		handle *browser.Handle
		want   string
	}{
		{"extension", &browser.Handle{Executable: "(extension)", Attached: true},
			"inside the browser through the extension (no external endpoint)"},
		{"launched", &browser.Handle{WSURL: "ws://127.0.0.1:9222/devtools/browser/x"},
			"ws://127.0.0.1:9222/devtools/browser/x"},
		{"attached", &browser.Handle{WSURL: "ws://127.0.0.1:9222/devtools/browser/x", Attached: true},
			"ws://127.0.0.1:9222/devtools/browser/x (attached)"},
	}
	for _, c := range cases {
		if got := cdpEndpoint(c.handle); got != c.want {
			t.Errorf("%s: cdpEndpoint = %q, want %q", c.name, got, c.want)
		}
	}
}

// status says how the session drives the browser; attach wins over the engine.
func TestEngineName(t *testing.T) {
	cases := []struct {
		attach, engine, want string
	}{
		{"", "", "ext"},
		{"", "chrome", "chrome"},
		{"host:9222", "ext", "attached (host:9222)"},
	}
	for _, c := range cases {
		if got := (&Agent{Attach: c.attach, Engine: c.engine}).engineName(); got != c.want {
			t.Errorf("engineName(attach=%q, engine=%q) = %q, want %q", c.attach, c.engine, got, c.want)
		}
	}
}

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
