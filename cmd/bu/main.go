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
	"syscall"

	"github.com/ajunior/browser-use/internal/cli"
	"github.com/ajunior/browser-use/internal/command"
	"github.com/ajunior/browser-use/internal/daemon"
	"github.com/ajunior/browser-use/internal/installer"
	"github.com/ajunior/browser-use/internal/mcpsrv"
	"github.com/ajunior/browser-use/internal/paths"
)

const version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
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
			Headless: envBool("BROWSER_USE_HEADLESS", false),
		})

	case "mcp":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		return mcpsrv.Run(ctx)

	case "install", "instalar":
		product := "chrome"
		if len(args) > 1 && (args[1] == "--headless" || args[1] == "headless") {
			product = "chrome-headless-shell"
		}
		_, err := installer.Install(context.Background(), installer.Options{Product: product})
		return err
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
