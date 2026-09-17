// axscope — agent-driven browser: one binary in three roles — client, daemon
// (serve) and MCP server (mcp) — plus install.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/command"
	"github.com/AndersonJ293/axscope/internal/daemon"
	"github.com/AndersonJ293/axscope/internal/daemonclient"
	"github.com/AndersonJ293/axscope/internal/mcpsrv"
	"github.com/AndersonJ293/axscope/internal/paths"
)

const version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]

	// Global mode flags, accepted before the command. The choice holds for the
	// whole session: the daemon brings up the browser with it.
	var mode string
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--chrome":
			mode = "chrome"
		case "--headless":
			mode = "headless"
		case "--ext":
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
		fmt.Printf("axscope %s\n", version)
		return nil

	case "serve":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		return daemon.Run(ctx, daemon.Options{
			Session:  paths.Session(),
			Attach:   os.Getenv("AXSCOPE_ATTACH"),
			Engine:   envOr("AXSCOPE_ENGINE", browser.EngineExt),
			Headless: envBool("AXSCOPE_HEADLESS", false),
		})

	case "mcp":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		return mcpsrv.Run(ctx)

	case "install":
		return runInstall(args[1:])

	case "engines":
		for _, e := range browser.DetectEngines() {
			mark := " "
			if e.Exists {
				mark = "*"
			}
			fmt.Printf("%s %-22s %s\n", mark, e.Product, e.Path)
		}
		return nil

	case "sessions":
		return runSessions()

	case "clean":
		return runClean(args[1:])

	case "stop":
		for _, a := range args[1:] {
			if a == "--all" || a == "all" {
				return runStopAll()
			}
		}
		// Without --all, falls into the normal path (stops only the current session).
	}

	req, err := command.Parse(args)
	if err != nil {
		return err
	}

	// `axscope script -` reads the script from stdin and sends the content, so as
	// not to depend on the daemon seeing the same directory.
	if req.Cmd == "script" && req.String("path") == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		req.Args["content"] = string(data)
		delete(req.Args, "path")
	}

	resp, err := daemonclient.Send(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "error:", resp.Error)
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
