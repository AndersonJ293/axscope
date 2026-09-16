// MCP server over stdio talking to the same daemon as the CLI, so both share one
// browser and one set of refs; JSON-RPC 2.0 per line, hand-rolled for zero deps.
package mcpsrv

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/AndersonJ293/axscope/internal/command"
	"github.com/AndersonJ293/axscope/internal/daemonclient"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

const protocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Run serves the MCP protocol on stdin/stdout until EOF.
func Run(ctx context.Context) error {
	reader := bufio.NewReaderSize(os.Stdin, 8<<20)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			handleLine(ctx, line, writer)
			writer.Flush()
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func handleLine(ctx context.Context, line []byte, writer *bufio.Writer) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return
	}
	var req rpcRequest
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return
	}
	// Notifications (without id) do not generate a response.
	if len(req.ID) == 0 {
		return
	}

	switch req.Method {
	case "initialize":
		// The MCP client name (opencode, claude, cursor…) names the tab group,
		// unless AXSCOPE_AGENT is set: an explicit choice wins over auto-detection.
		var params struct {
			ClientInfo struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &params)
		if name := displayName(params.ClientInfo.Name); name != "" && os.Getenv("AXSCOPE_AGENT") == "" {
			_ = os.Setenv("AXSCOPE_AGENT", name)
		}
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "axscope", "version": "0.1.0"},
		}})

	case "ping":
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})

	case "tools/list":
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools()}})

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			write(writer, errResponse(req.ID, -32602, "invalid params"))
			return
		}
		result := callTool(ctx, params.Name, params.Arguments)
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})

	case "resources/list":
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resources": []any{}}})

	case "prompts/list":
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"prompts": []any{}}})

	default:
		write(writer, errResponse(req.ID, -32601, "unsupported method: "+req.Method))
	}
}

func callTool(ctx context.Context, name string, args map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	resp, err := daemonclient.Send(protocol.Request{Cmd: name, Args: args})
	if err != nil {
		return toolText("error: "+err.Error(), true)
	}
	if !resp.OK {
		return toolText("error: "+resp.Error, true)
	}
	if b64 := resp.Image; b64 != nil {
		return map[string]any{"content": []map[string]any{
			{"type": "text", "text": resp.Text},
			{"type": "image", "data": b64.Base64, "mimeType": b64.MimeType},
		}}
	}
	return toolText(resp.Text, false)
}

func toolText(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// curatedMCP is the lean set exposed by default (fewer schemas means less context
// per request; `AXSCOPE_MCP_TOOLS=all` opens everything). It covers reading,
// interaction, scrolling, navigation and tab lifecycle; select/check/uncheck/
// type/upload stay because the refusal messages cite them.
var curatedMCP = map[string]bool{
	"open":     true,
	"snap":     true,
	"click":    true,
	"hover":    true,
	"drag":     true,
	"fill":     true,
	"type":     true,
	"select":   true,
	"check":    true,
	"uncheck":  true,
	"upload":   true,
	"press":    true,
	"wait":     true,
	"waitgone": true,
	"scroll":   true,
	"read":     true,
	"tabs":     true,
	"tab":      true,
	"newtab":   true,
	"closetab": true,
	"back":     true,
	"forward":  true,
	"reload":   true,
	"shot":     true,
	"script":   true,
	"console":  true,
	"net":      true,
	"eval":     true,
}

// tools derives the tools from the command table, so CLI and MCP do not diverge.
func tools() []toolDef {
	all := os.Getenv("AXSCOPE_MCP_TOOLS") == "all"
	out := make([]toolDef, 0, len(command.Specs))
	for _, spec := range command.Specs {
		switch spec.Cmd {
		case "ping", "stop", "install":
			continue
		}
		if !all && !curatedMCP[spec.Cmd] {
			continue
		}
		props := map[string]any{}
		optional := map[string]bool{}
		for _, o := range spec.Optional {
			optional[o] = true
		}
		var required []string
		for _, p := range spec.Positional {
			props[p] = map[string]any{"type": "string"}
			if !optional[p] {
				required = append(required, p)
			}
		}
		for _, f := range spec.Flags {
			props[f] = map[string]any{"type": "boolean"}
		}
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		out = append(out, toolDef{
			Name:        spec.Cmd,
			Description: spec.Help,
			InputSchema: schema,
		})
	}
	return out
}

func write(writer *bufio.Writer, resp rpcResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = writer.Write(append(data, '\n'))
}

// displayName turns the MCP client name into a readable label:
// "opencode" -> "Opencode", "claude-desktop" -> "Claude Desktop".
func displayName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func errResponse(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}
