// Parser de comandos compartilhado por CLI, daemon (roteiro) e MCP.
//
// Gramática simples e sem ambiguidade:
//
//	<comando> [posicional] [chave=valor] [--flag]
//
// O primeiro posicional mapeia para o primeiro nome do spec, e assim por diante.
// Flags são booleanas; valores usam chave=valor ou posicional.
package command

import (
	"fmt"
	"strings"

	"github.com/ajunior/browser-use/internal/protocol"
)

// Spec descreve um comando.
type Spec struct {
	Cmd        string
	Positional []string
	// Optional lista posicionais que podem ser omitidos (têm padrão).
	Optional []string
	Flags    []string
	Help     string
}

// Specs é a tabela canônica. A ordem não importa.
var Specs = []Spec{
	{Cmd: "ping", Help: "verifica se o daemon responde"},
	{Cmd: "status", Help: "URL, título, abas e estado do overlay"},
	{Cmd: "install", Flags: []string{"engine"}, Help: "baixa motores (--engine chrome|shell|all)"},
	{Cmd: "engines", Help: "lista os motores instalados"},
	{Cmd: "clean", Flags: []string{"tudo"}, Help: "apaga logs e sessões mortas (--tudo inclui perfis e browsers)"},
	{Cmd: "stop", Help: "encerra o daemon (e o browser, se fomos nós que subimos)"},

	{Cmd: "open", Positional: []string{"url"}, Flags: []string{"new"}, Help: "abre/navega (--new abre em aba nova)"},
	{Cmd: "snap", Flags: []string{"refs", "tudo"}, Help: "lê a tela em texto (--refs só alvos; --tudo inclui rodapé e atalhos)"},
	{Cmd: "click", Positional: []string{"target"}, Flags: []string{"right", "middle", "double"}, Help: "clica no alvo (ref/css=/text=)"},
	{Cmd: "hover", Positional: []string{"target"}, Help: "passa o mouse no alvo"},
	{Cmd: "drag", Positional: []string{"from", "to"}, Flags: []string{"topo", "base", "tipo"}, Help: "arrasta <de> até <para> (--topo/--base onde soltar; tipo=ponteiro|html5 força a família)"},
	{Cmd: "fill", Positional: []string{"target", "text"}, Help: "substitui o conteúdo do campo"},
	{Cmd: "type", Positional: []string{"target", "text"}, Help: "digita caractere a caractere"},
	{Cmd: "press", Positional: []string{"key"}, Help: "envia tecla/atalho (Enter, Control+A)"},
	{Cmd: "select", Positional: []string{"target", "value"}, Help: "escolhe opção de <select>"},
	{Cmd: "check", Positional: []string{"target"}, Help: "marca checkbox/radio"},
	{Cmd: "uncheck", Positional: []string{"target"}, Help: "desmarca checkbox/radio"},
	{Cmd: "scroll", Positional: []string{"dy"}, Help: "rola o viewport (dy positivo desce)"},
	{Cmd: "wait", Positional: []string{"text", "timeout"}, Optional: []string{"timeout"}, Help: "espera o texto aparecer (timeout em ms)"},
	{Cmd: "waitgone", Positional: []string{"text", "timeout"}, Optional: []string{"timeout"}, Help: "espera o texto sumir (timeout em ms)"},
	{Cmd: "read", Positional: []string{"selector"}, Optional: []string{"selector"}, Help: "lê o texto principal da página"},
	{Cmd: "eval", Positional: []string{"js"}, Help: "avalia JavaScript na página"},
	{Cmd: "tabs", Help: "lista as abas"},
	{Cmd: "tab", Positional: []string{"ref"}, Flags: []string{"focus"}, Help: "troca para a aba (índice ou targetId; --focus traz a janela à frente)"},
	{Cmd: "newtab", Positional: []string{"url"}, Optional: []string{"url"}, Help: "abre aba nova"},
	{Cmd: "closetab", Positional: []string{"ref"}, Help: "fecha a aba"},
	{Cmd: "back", Help: "volta no histórico"},
	{Cmd: "forward", Help: "avança no histórico"},
	{Cmd: "reload", Help: "recarrega a página"},
	{Cmd: "console", Flags: []string{"all"}, Help: "erros/avisos do console"},
	{Cmd: "net", Positional: []string{"filter"}, Optional: []string{"filter"}, Help: "requisições de rede"},
	{Cmd: "shot", Positional: []string{"path"}, Optional: []string{"path"}, Flags: []string{"full"}, Help: "captura PNG (sem caminho vai para /tmp)"},
	{Cmd: "dialog", Positional: []string{"action"}, Help: "accept|dismiss o próximo diálogo"},
	{Cmd: "script", Positional: []string{"path"}, Help: "executa um roteiro (arquivo ou - para stdin)"},
}

var aliasToCmd = map[string]string{
	"estado":     "status",
	"abrir":      "open",
	"tela":       "snap",
	"clicar":     "click",
	"passar":     "hover",
	"arrastar":   "drag",
	"digitar":    "fill",
	"preencher":  "fill",
	"teclar":     "type",
	"tecla":      "press",
	"escolher":   "select",
	"marcar":     "check",
	"desmarcar":  "uncheck",
	"rolar":      "scroll",
	"esperar":    "wait",
	"sumir":      "waitgone",
	"ler":        "read",
	"abas":       "tabs",
	"nova":       "newtab",
	"fecharaba":  "closetab",
	"voltar":     "back",
	"avancar":    "forward",
	"recarregar": "reload",
	"rede":       "net",
	"captura":    "shot",
	"roteiro":    "script",
	"encerrar":   "stop",
	"instalar":   "install",
}

func lookup(cmd string) (Spec, bool) {
	if canonical, ok := aliasToCmd[cmd]; ok {
		cmd = canonical
	}
	for _, s := range Specs {
		if s.Cmd == cmd {
			return s, true
		}
	}
	return Spec{}, false
}

// Parse transforma tokens em um pedido. Aceita aliases em português.
func Parse(tokens []string) (protocol.Request, error) {
	if len(tokens) == 0 {
		return protocol.Request{}, fmt.Errorf("nenhum comando")
	}
	spec, ok := lookup(tokens[0])
	if !ok {
		return protocol.Request{}, fmt.Errorf("comando desconhecido: %q (veja `bu help`)", tokens[0])
	}
	req := protocol.Request{Cmd: spec.Cmd, Args: map[string]any{}}

	positional := append([]string(nil), spec.Positional...)
	known := map[string]bool{}
	for _, p := range spec.Positional {
		known[p] = true
	}
	for _, f := range spec.Flags {
		known[f] = true
	}

	for _, token := range tokens[1:] {
		switch {
		case strings.HasPrefix(token, "--"):
			name := strings.TrimPrefix(token, "--")
			if !known[name] {
				return protocol.Request{}, fmt.Errorf("comando %q não tem a flag --%s", spec.Cmd, name)
			}
			req.Args[name] = true
		// `k=v` só vale quando `k` é um argumento conhecido — assim um valor
		// livre (JS, texto com "=") cai como posicional, não como par.
		case strings.Contains(token, "=") && !strings.HasPrefix(token, "=") &&
			known[strings.SplitN(token, "=", 2)[0]]:
			parts := strings.SplitN(token, "=", 2)
			req.Args[parts[0]] = parts[1]
		default:
			if len(positional) == 0 {
				return protocol.Request{}, fmt.Errorf("sobra argumento em %q: %q", spec.Cmd, token)
			}
			req.Args[positional[0]] = token
			positional = positional[1:]
		}
	}
	return req, nil
}

// Help devolve o texto de ajuda.
func Help() string {
	var b strings.Builder
	b.WriteString("bu — browser dirigido por agente\n\n")
	b.WriteString("uso: bu <comando> [args] [chave=valor] [--flag]\n")
	b.WriteString("flags globais: (padrão) extensão no Brave | --ver (Chrome dedicado) | --leve (sem janela)\n\n")
	for _, s := range Specs {
		line := "  " + s.Cmd
		for _, p := range s.Positional {
			line += " <" + p + ">"
		}
		for _, f := range s.Flags {
			line += " [--" + f + "]"
		}
		b.WriteString(line + "\n      " + s.Help + "\n")
	}
	return b.String()
}
