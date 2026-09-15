// Servidor MCP sobre stdio, falando com o mesmo daemon da CLI — assim MCP e CLI
// compartilham um único browser e um único conjunto de refs.
//
// JSON-RPC 2.0 por linha (transporte stdio do MCP), implementado à mão para não
// carregar dependência nenhuma.
package mcpsrv

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/ajunior/browser-use/internal/cli"
	"github.com/ajunior/browser-use/internal/command"
	"github.com/ajunior/browser-use/internal/protocol"
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

// Run atende o protocolo MCP no stdin/stdout até o EOF.
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
	// Notificações (sem id) não geram resposta.
	if len(req.ID) == 0 {
		return
	}

	switch req.Method {
	case "initialize":
		// O nome do cliente MCP (opencode, claude, cursor…) vira o nome do grupo
		// de abas no navegador. Sem configurar nada.
		var params struct {
			ClientInfo struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &params)
		if name := displayName(params.ClientInfo.Name); name != "" {
			_ = os.Setenv("BROWSER_USE_AGENT", name)
		}
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "browser-use", "version": "0.2.0"},
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
			write(writer, errResponse(req.ID, -32602, "parâmetros inválidos"))
			return
		}
		result := callTool(ctx, params.Name, params.Arguments)
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})

	case "resources/list":
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resources": []any{}}})

	case "prompts/list":
		write(writer, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"prompts": []any{}}})

	default:
		write(writer, errResponse(req.ID, -32601, "método não suportado: "+req.Method))
	}
}

func callTool(ctx context.Context, name string, args map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	resp, err := cli.Send(protocol.Request{Cmd: name, Args: args})
	if err != nil {
		return toolText("erro: "+err.Error(), true)
	}
	if !resp.OK {
		return toolText("erro: "+resp.Error, true)
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

// curatedMCP é o conjunto enxuto exposto por padrão: alta frequência, e o resto
// via `script`. Schema de ferramenta custa contexto em toda requisição, então
// menos ferramentas é melhor — `BROWSER_USE_MCP_TOOLS=all` abre tudo.
var curatedMCP = map[string]bool{
	"open":     true,
	"snap":     true,
	"click":    true,
	"fill":     true,
	"press":    true,
	"wait":     true,
	"waitgone": true,
	"read":     true,
	"tabs":     true,
	"tab":      true,
	"back":     true,
	"shot":     true,
	"script":   true,
	"console":  true,
	"net":      true,
	"eval":     true,
}

// tools deriva as ferramentas da tabela de comandos, para CLI e MCP não divergirem.
func tools() []toolDef {
	all := os.Getenv("BROWSER_USE_MCP_TOOLS") == "all"
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

// displayName transforma o nome do cliente MCP num rótulo legível:
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
