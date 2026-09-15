// bu — browser dirigido por agente.
//
// Um único binário, três papéis:
//
//	bu <comando>   → cliente: fala com o daemon (subindo-o se preciso)
//	bu serve       → o daemon (browser vivo, socket unix)
//	bu mcp         → servidor MCP sobre stdio, apontando para o mesmo daemon
//	bu install     → baixa o Chrome for Testing
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/cli"
	"github.com/ajunior/browser-use/internal/command"
	"github.com/ajunior/browser-use/internal/daemon"
	"github.com/ajunior/browser-use/internal/installer"
	"github.com/ajunior/browser-use/internal/mcpsrv"
	"github.com/ajunior/browser-use/internal/paths"
	"github.com/ajunior/browser-use/internal/protocol"
)

const version = "0.2.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]

	// Flags globais de modo, aceitas antes do comando:
	//   --ver  → janela de verdade (Chrome for Testing), sessão "ver"
	//   --leve → sem janela (chrome-headless-shell), padrão
	// A escolha vale para a sessão inteira: o daemon sobe o browser com ela.
	var mode string
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--ver", "--watch":
			mode = "ver"
		case "--leve", "--light":
			mode = "leve"
		case "--ext", "--brave", "--extensao":
			mode = "ext"
		default:
			filtered = append(filtered, a)
		}
	}
	args = filtered
	applyMode(mode)

	if len(args) == 0 {
		fmt.Print(command.Help())
		return nil
	}

	switch args[0] {
	case "help", "--help", "-h":
		fmt.Print(command.Help())
		return nil
	case "version", "--version", "-v":
		fmt.Printf("browser-use %s\n", version)
		return nil

	case "serve":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		return daemon.Run(ctx, daemon.Options{
			Session:  paths.Session(),
			Attach:   os.Getenv("BROWSER_USE_ATTACH"),
			Engine:   envOr("BROWSER_USE_ENGINE", browser.EngineExt),
			Headless: envBool("BROWSER_USE_HEADLESS", false),
		})

	case "mcp":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		return mcpsrv.Run(ctx)

	case "install", "instalar":
		return runInstall(args[1:])

	case "engines", "motores":
		for _, e := range browser.DetectEngines() {
			mark := " "
			if e.Exists {
				mark = "*"
			}
			fmt.Printf("%s %-22s %s\n", mark, e.Product, e.Path)
		}
		return nil

	case "clean", "limpar":
		return runClean(args[1:])

	case "stop", "encerrar":
		for _, a := range args[1:] {
			if a == "--all" || a == "all" {
				return runStopAll()
			}
		}
		// Sem --all, cai no caminho normal (encerra só a sessão atual).
	}

	req, err := command.Parse(args)
	if err != nil {
		return err
	}

	// `bu script -` lê o roteiro do stdin e manda o conteúdo, para não depender
	// do daemon enxergar o mesmo diretório.
	if req.Cmd == "script" && req.String("path") == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		req.Args["content"] = string(data)
		delete(req.Args, "path")
	}

	resp, err := cli.Send(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "erro:", resp.Error)
		os.Exit(2)
	}
	fmt.Println(resp.Text)
	return nil
}

func envBool(key string, def bool) bool {
	switch os.Getenv(key) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// applyMode ajusta o motor (e a sessão, para os modos coexistirem).
func applyMode(mode string) {
	switch mode {
	case "ver":
		_ = os.Setenv("BROWSER_USE_ENGINE", browser.EngineChrome)
		if os.Getenv("BROWSER_USE_SESSION") == "" {
			_ = os.Setenv("BROWSER_USE_SESSION", "ver")
		}
	case "ext":
		_ = os.Setenv("BROWSER_USE_ENGINE", browser.EngineExt)
		if os.Getenv("BROWSER_USE_SESSION") == "" {
			_ = os.Setenv("BROWSER_USE_SESSION", "ext")
		}
	case "leve":
		_ = os.Setenv("BROWSER_USE_ENGINE", browser.EngineShell)
		if os.Getenv("BROWSER_USE_SESSION") == "" {
			_ = os.Setenv("BROWSER_USE_SESSION", "leve")
		}
	}
}

// runStopAll encerra todos os daemons vivos (e os browsers que eles subiram).
func runStopAll() error {
	dir := filepath.Join(paths.StateDir(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Println("nenhuma sessão ativa")
		return nil
	}
	stopped := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		session := strings.TrimSuffix(e.Name(), ".json")
		socketPath := paths.SocketPath(session)
		if _, err := os.Stat(socketPath); err != nil {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		if _, err := cli.SendTo(socketPath, protocol.Request{Cmd: "stop"}); err == nil {
			fmt.Printf("encerrei a sessão %q\n", session)
			stopped++
		}
	}
	if stopped == 0 {
		fmt.Println("nenhuma sessão ativa")
	}
	return nil
}

// runClean apaga o que é descartável. Sem --tudo, preserva perfil (logins) e os
// browsers baixados, que são o que dá trabalho para refazer.
func runClean(args []string) error {
	tudo := false
	for _, a := range args {
		if a == "--tudo" || a == "tudo" {
			tudo = true
		}
	}

	targets := []string{filepath.Join(paths.StateDir(), "logs")}
	if tudo {
		targets = append(targets,
			filepath.Join(paths.StateDir(), "profiles"),
			filepath.Join(paths.StateDir(), "browsers"),
		)
	}

	var freed int64
	for _, dir := range targets {
		size := dirSize(dir)
		if size == 0 {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "não consegui remover %s: %v\n", dir, err)
			continue
		}
		freed += size
		fmt.Printf("removi %-24s %s\n", filepath.Base(dir), human(size))
	}

	// Sessões cujo socket já não existe são lixo.
	sessionsDir := filepath.Join(paths.StateDir(), "sessions")
	if entries, err := os.ReadDir(sessionsDir); err == nil {
		for _, e := range entries {
			session := strings.TrimSuffix(e.Name(), ".json")
			if _, err := os.Stat(paths.SocketPath(session)); err != nil {
				_ = os.Remove(filepath.Join(sessionsDir, e.Name()))
				fmt.Printf("removi sessão %q (sem daemon)\n", session)
			}
		}
	}

	if freed == 0 {
		fmt.Println("nada a limpar")
	} else {
		fmt.Printf("liberado: %s\n", human(freed))
	}
	return nil
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func human(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// runInstall baixa os motores pedidos. `--engine` aceita chrome, shell ou all.
func runInstall(args []string) error {
	engine := "chrome"
	for i, a := range args {
		if (a == "--engine" || a == "--motor") && i+1 < len(args) {
			engine = args[i+1]
		} else if a == "shell" || a == "headless" {
			engine = "shell"
		}
	}
	var products []string
	switch engine {
	case "shell", "headless", "chrome-headless-shell":
		products = []string{"chrome-headless-shell"}
	case "all", "todos":
		products = []string{"chrome", "chrome-headless-shell"}
	default:
		products = []string{"chrome"}
	}
	for _, product := range products {
		if _, err := installer.Install(context.Background(), installer.Options{Product: product}); err != nil {
			return err
		}
	}
	return nil
}
