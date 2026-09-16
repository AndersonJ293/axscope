// Protocol between CLI/MCP and the daemon: one request and one JSON response
// per line on a unix socket.
package protocol

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Request is what the client sends.
type Request struct {
	ID   int64          `json:"id,omitempty"`
	Cmd  string         `json:"cmd"`
	Args map[string]any `json:"args,omitempty"`
	// Agent identifies the driver (MCP client or AXSCOPE_AGENT), naming the browser tab group.
	Agent string `json:"agent,omitempty"`
}

// Image is an optional attachment (e.g. screenshot) for clients that accept images.
type Image struct {
	MimeType string `json:"mimeType"`
	Base64   string `json:"base64"`
}

// Response is what the daemon returns.
type Response struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
	Image *Image `json:"image,omitempty"`
}

// Fail builds an error response.
func Fail(err error) Response {
	if err == nil {
		return Response{OK: false, Error: "unknown error"}
	}
	return Response{OK: false, Error: err.Error()}
}

// String returns an argument as a string.
func (r Request) String(key string) string {
	if r.Args == nil {
		return ""
	}
	v, ok := r.Args[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// Non-string values fall back to their JSON text.
	b, _ := json.Marshal(v)
	return string(b)
}

// Bool returns a boolean argument with a default.
func (r Request) Bool(key string, def bool) bool {
	if r.Args == nil {
		return def
	}
	if v, ok := r.Args[key].(bool); ok {
		return v
	}
	return def
}

// Int returns an integer argument with a default.
func (r Request) Int(key string, def int) int {
	if r.Args == nil {
		return def
	}
	switch v := r.Args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	case string:
		// CLI and MCP send options as text; without this a numeric option is inert.
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return def
}
