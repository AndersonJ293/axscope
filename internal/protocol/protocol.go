// Protocolo entre CLI/MCP e o daemon: um pedido e uma resposta JSON por linha
// num socket unix.
package protocol

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Request é o que o cliente manda.
type Request struct {
	ID   int64          `json:"id,omitempty"`
	Cmd  string         `json:"cmd"`
	Args map[string]any `json:"args,omitempty"`
	// Agent identifica quem está dirigindo (ex.: "Opencode"), para nomear o
	// grupo de abas no navegador. Vem do cliente MCP ou de AXSCOPE_AGENT.
	Agent string `json:"agent,omitempty"`
}

// Image é um anexo opcional (ex.: screenshot) para clientes que aceitam imagem.
type Image struct {
	MimeType string `json:"mimeType"`
	Base64   string `json:"base64"`
}

// Response é o que o daemon devolve.
type Response struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
	Image *Image `json:"image,omitempty"`
}

// Fail monta uma resposta de erro.
func Fail(err error) Response {
	if err == nil {
		return Response{OK: false, Error: "erro desconhecido"}
	}
	return Response{OK: false, Error: err.Error()}
}

// String devolve um argumento como string.
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
	b, _ := json.Marshal(v)
	return string(b)
}

// Bool devolve um argumento booleano com default.
func (r Request) Bool(key string, def bool) bool {
	if r.Args == nil {
		return def
	}
	if v, ok := r.Args[key].(bool); ok {
		return v
	}
	return def
}

// Int devolve um argumento inteiro com default.
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
		// Valor vindo de `k=v` ou posicional chega como string (CLI e MCP
		// mandam texto). Sem isto, opção numérica era inerte.
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return def
}
