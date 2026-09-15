// axscope — agent-driven browser.
//
// A single binary, three roles:
//
//	axscope <command>   → client: talks to the daemon (bringing it up if needed)
//	axscope serve       → the daemon (live browser, unix socket)
//	axscope mcp         → MCP server over stdio, pointing at the same daemon
//	axscope install     → downloads Chrome for Testing
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

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/command"
	"github.com/AndersonJ293/axscope/internal/daemon"
	"github.com/AndersonJ293/axscope/internal/daemonclient"
	"github.com/AndersonJ293/axscope/internal/installer"
	"github.com/AndersonJ293/axscope/internal/mcpsrv"
	"github.com/AndersonJ293/axscope/internal/paths"
	"github.com/AndersonJ293/axscope/internal/protocol"
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

	// Global mode flags, accepted before the command:
	//   --chrome   → a real window (Chrome for Testing), session "chrome"
	//   --headless → no window (chrome-headless-shell), default
	// The choice holds for the whole session: the daemon brings up the browser
	// with it.
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

// applyMode adjusts the engine (and the session, so the modes coexist).
func applyMode(mode string) {
	switch mode {
	case "chrome":
		_ = os.Setenv("AXSCOPE_ENGINE", browser.EngineChrome)
		if os.Getenv("AXSCOPE_SESSION") == "" {
			_ = os.Setenv("AXSCOPE_SESSION", "chrome")
		}
	case "ext":
		_ = os.Setenv("AXSCOPE_ENGINE", browser.EngineExt)
		if os.Getenv("AXSCOPE_SESSION") == "" {
			_ = os.Setenv("AXSCOPE_SESSION", "ext")
		}
	case "headless":
		_ = os.Setenv("AXSCOPE_ENGINE", browser.EngineShell)
		if os.Getenv("AXSCOPE_SESSION") == "" {
			_ = os.Setenv("AXSCOPE_SESSION", "headless")
		}
	}
}

// runStopAll stops all live daemons (and the browsers they started).
func runStopAll() error {
	dir := filepath.Join(paths.StateDir(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Println("no active sessions")
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
		if _, err := daemonclient.SendTo(socketPath, protocol.Request{Cmd: "stop"}); err == nil {
			fmt.Printf("stopped session %q\n", session)
			stopped++
		}
	}
	if stopped == 0 {
		fmt.Println("no active sessions")
	}
	return nil
}

// runClean deletes what is disposable. Without --all, it preserves the profile
// (logins) and the downloaded browsers, which are what takes work to redo.
func runClean(args []string) error {
	all := false
	for _, a := range args {
		if a == "--all" || a == "all" {
			all = true
		}
	}

	targets := []string{filepath.Join(paths.StateDir(), "logs")}
	if all {
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
			fmt.Fprintf(os.Stderr, "could not remove %s: %v\n", dir, err)
			continue
		}
		freed += size
		fmt.Printf("removed %-24s %s\n", filepath.Base(dir), human(size))
	}

	// Sessions whose socket no longer exists are garbage.
	sessionsDir := filepath.Join(paths.StateDir(), "sessions")
	if entries, err := os.ReadDir(sessionsDir); err == nil {
		for _, e := range entries {
			session := strings.TrimSuffix(e.Name(), ".json")
			if _, err := os.Stat(paths.SocketPath(session)); err != nil {
				_ = os.Remove(filepath.Join(sessionsDir, e.Name()))
				fmt.Printf("removed session %q (no daemon)\n", session)
			}
		}
	}

	if freed == 0 {
		fmt.Println("nothing to clean")
	} else {
		fmt.Printf("freed: %s\n", human(freed))
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

// runInstall downloads the requested engines. `--engine` accepts chrome, shell or all.
func runInstall(args []string) error {
	engine := "chrome"
	for i, a := range args {
		if a == "--engine" && i+1 < len(args) {
			engine = args[i+1]
		} else if a == "shell" || a == "headless" {
			engine = "shell"
		}
	}
	var products []string
	switch engine {
	case "shell", "headless", "chrome-headless-shell":
		products = []string{"chrome-headless-shell"}
	case "all":
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
