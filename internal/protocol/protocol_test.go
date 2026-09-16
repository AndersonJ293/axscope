package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Int must accept text because CLI and MCP send numeric options as strings;
// without the `string` case every numeric option was inert.
func TestInt(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		def  int
		want int
	}{
		{"CLI string", map[string]any{"timeout": "5000"}, 0, 5000},
		{"string with space", map[string]any{"timeout": " 700 "}, 0, 700},
		{"invalid string falls back to default", map[string]any{"timeout": "abc"}, 42, 42},
		{"float from JSON", map[string]any{"timeout": float64(1500)}, 0, 1500},
		{"direct int", map[string]any{"timeout": 3}, 0, 3},
		{"json.Number", map[string]any{"timeout": json.Number("2500")}, 0, 2500},
		{"invalid json.Number falls back", map[string]any{"timeout": json.Number("nope")}, 7, 7},
		{"wrong type falls back", map[string]any{"timeout": true}, 5, 5},
		{"negative string", map[string]any{"timeout": "-3"}, 0, -3},
		{"absent", map[string]any{}, 9, 9},
		{"nil args", nil, 9, 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Request{Cmd: "wait", Args: c.args}
			if v := r.Int("timeout", c.def); v != c.want {
				t.Errorf("Int = %d, want %d", v, c.want)
			}
		})
	}
}

// String returns the text a target/value argument carries. A missing key, a nil
// value and nil args all mean "not sent"; a non-string is rendered as JSON text
// so a boolean or a number never silently becomes the empty string.
func TestString(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing key", map[string]any{"other": "x"}, ""},
		{"nil value", map[string]any{"target": nil}, ""},
		{"nil args", nil, ""},
		{"string", map[string]any{"target": "e12#3"}, "e12#3"},
		{"empty string", map[string]any{"target": ""}, ""},
		{"number becomes JSON text", map[string]any{"target": float64(3)}, "3"},
		{"bool becomes JSON text", map[string]any{"target": true}, "true"},
		{"slice becomes JSON text", map[string]any{"target": []any{"a", "b"}}, `["a","b"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Request{Cmd: "click", Args: c.args}
			if got := r.String("target"); got != c.want {
				t.Errorf("String = %q, want %q", got, c.want)
			}
		})
	}
}

// Bool returns the flag with the default; anything that is not a bool (a CLI
// "true" string included) leaves the default untouched, which is what makes
// --flag safe to parse.
func TestBool(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		def  bool
		want bool
	}{
		{"missing key keeps default true", map[string]any{"other": true}, true, true},
		{"missing key keeps default false", map[string]any{}, false, false},
		{"nil args keeps default", nil, true, true},
		{"true", map[string]any{"double": true}, false, true},
		{"false", map[string]any{"double": false}, true, false},
		{"wrong type string keeps default", map[string]any{"double": "true"}, false, false},
		{"wrong type number keeps default", map[string]any{"double": float64(1)}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Request{Cmd: "click", Args: c.args}
			if got := r.Bool("double", c.def); got != c.want {
				t.Errorf("Bool = %v, want %v", got, c.want)
			}
		})
	}
}

// The request and the response cross a JSON line in both directions; a field
// lost in the tags would silently break a command on the wire.
func TestRequestResponseJSONRoundTrip(t *testing.T) {
	req := Request{
		ID:    7,
		Cmd:   "click",
		Args:  map[string]any{"target": "e1#2", "double": true},
		Agent: "Opencode",
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var gotReq Request
	if err := json.Unmarshal(data, &gotReq); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if !reflect.DeepEqual(gotReq, req) {
		t.Errorf("request round-trip diverged:\n got %#v\nwant %#v", gotReq, req)
	}

	resp := Response{
		OK:    true,
		Text:  "clicked",
		Image: &Image{MimeType: "image/png", Base64: "QUFB"},
	}
	data, err = json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var gotResp Response
	if err := json.Unmarshal(data, &gotResp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !reflect.DeepEqual(gotResp, resp) {
		t.Errorf("response round-trip diverged:\n got %#v\nwant %#v", gotResp, resp)
	}

	// An omitted image must not materialize as an empty one on the other side.
	minimalJSON, err := json.Marshal(Response{OK: true, Text: "pong"})
	if err != nil {
		t.Fatalf("marshal minimal response: %v", err)
	}
	var minimal Response
	if err := json.Unmarshal(minimalJSON, &minimal); err != nil {
		t.Fatalf("unmarshal minimal response: %v", err)
	}
	if minimal.Image != nil || minimal.Error != "" {
		t.Errorf("omitempty fields came back non-zero: %#v", minimal)
	}
}

// Fail carries the error text and stays OK=false; a nil error still produces a
// usable response instead of a panic.
func TestFail(t *testing.T) {
	got := Fail(errBoom)
	if got.OK || got.Error != "boom" {
		t.Errorf("Fail = %#v, want OK=false error=boom", got)
	}
	if got := Fail(nil); got.OK || got.Error == "" {
		t.Errorf("Fail(nil) = %#v, want a non-empty error", got)
	}
}

var errBoom = &plainError{"boom"}

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }
