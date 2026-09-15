package protocol

import "testing"

// Regression: a value coming from the CLI or MCP is text, not a number.
// Without the `string` case, every numeric option (wait timeout) was inert:
// Request.Int returned the default and nobody noticed.
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
