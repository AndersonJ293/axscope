// Finds, starts and connects to Chromium: `Launch` starts a browser with its own
// profile and reads the CDP URL from stderr, `Attach` connects to an open one.
package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/paths"
)

var devToolsRe = regexp.MustCompile(`DevTools listening on (ws://\S+)`)

// Supported engines.
const (
	// EngineChrome is Chrome for Testing: a real window, a visible cursor.
	EngineChrome = "chrome"
	// EngineShell is chrome-headless-shell: the same engine and the same CDP,
	// without a window — lighter, but with no rendered cursor.
	EngineShell = "shell"
	// EngineExt drives the user's browser (Brave) through the extension, via
	// chrome.debugger: a real profile, with the logins already made.
	EngineExt = "ext"
)

var systemCandidates = []string{
	"google-chrome-stable",
	"google-chrome",
	"chromium",
	"chromium-browser",
	"brave",
	"brave-browser",
	"microsoft-edge-stable",
	"microsoft-edge",
}

// LaunchOptions describes how to start the browser.
type LaunchOptions struct {
	Session    string
	Engine     string
	Headless   bool
	Executable string
	WindowSize string
	ExtraArgs  []string
	// Fixed debug port; 0 chooses a free one (read from stderr).
	Port int
}

// Handle is a browser ready for use.
type Handle struct {
	Client     *cdp.Client
	WSURL      string
	Executable string
	Attached   bool
	ProfileDir string
	Cmd        *exec.Cmd
}

func engineProduct(engine string) string {
	if engine == EngineShell {
		return "chrome-headless-shell"
	}
	return "chrome"
}

// ResolveExecutable finds Chromium: flag, env, downloaded by `axscope install`,
// system.
func ResolveExecutable(engine, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv("AXSCOPE_CHROME"); v != "" {
		return v, nil
	}
	product := engineProduct(engine)
	if marker, err := os.ReadFile(paths.BrowserExecutableMarker(product)); err == nil {
		p := strings.TrimSpace(string(marker))
		if p != "" {
			if _, statErr := os.Stat(p); statErr == nil {
				return p, nil
			}
		}
	}
	if engine == EngineShell {
		return "", fmt.Errorf("chrome-headless-shell not installed: run `axscope install --engine shell`")
	}
	for _, name := range systemCandidates {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Chromium found: run `axscope install` or set AXSCOPE_CHROME")
}

// Engine is an engine available on the system.
type Engine struct {
	Product string
	Path    string
	Exists  bool
}

// DetectEngines lists the installed engines (downloaded and system).
func DetectEngines() []Engine {
	var out []Engine
	for _, product := range []string{"chrome", "chrome-headless-shell"} {
		e := Engine{Product: product}
		if marker, err := os.ReadFile(paths.BrowserExecutableMarker(product)); err == nil {
			e.Path = strings.TrimSpace(string(marker))
			if _, statErr := os.Stat(e.Path); statErr == nil {
				e.Exists = true
			}
		}
		out = append(out, e)
	}
	for _, name := range systemCandidates {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, Engine{Product: name, Path: p, Exists: true})
		}
	}
	return out
}

func buildArgs(opts LaunchOptions, profile string) []string {
	size := opts.WindowSize
	if size == "" {
		size = "1440,960"
	}
	args := []string{
		"--remote-debugging-port=" + fmt.Sprint(opts.Port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-default-apps",
		"--disable-sync",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-domain-reliability",
		"--disable-client-side-phishing-detection",
		"--disable-search-engine-choice-screen",
		"--disable-features=Translate,OptimizationHints,MediaRouter,PrivacySandboxSettings4,CalculateNativeWinOcclusion",
		"--password-store=basic",
		"--use-mock-keychain",
		"--metrics-recording-only",
		"--no-pings",
		"--force-color-profile=srgb",
		"--hide-crash-restore-bubble",
		"--window-size=" + size,
		"--window-position=60,60",
	}
	// chrome-headless-shell is already headless: it does not get --headless=new.
	if opts.Headless && opts.Engine != EngineShell {
		args = append(args, "--headless=new")
	}
	if opts.Headless || opts.Engine == EngineShell {
		args = append(args, "--disable-gpu")
	}
	// The Accessibility domain turns the tree on on demand; forcing it at startup
	// costs memory in every process.
	if envBool("AXSCOPE_FORCE_AX", false) {
		args = append(args, "--force-renderer-accessibility")
	}
	args = append(args, opts.ExtraArgs...)
	return args
}

// ensureProfilePrefs makes startup deterministic, without restoring the previous
// runs' tabs.
func ensureProfilePrefs(profile string) {
	prefsPath := filepath.Join(profile, "Default", "Preferences")
	if err := os.MkdirAll(filepath.Dir(prefsPath), 0o755); err != nil {
		return
	}
	prefs := map[string]any{}
	if data, err := os.ReadFile(prefsPath); err == nil {
		if err := json.Unmarshal(data, &prefs); err != nil {
			return // leave JSON that cannot be parsed untouched
		}
	}
	prefs["profile"] = withKey(prefs["profile"], "exit_type", "Normal")
	prefs["session"] = withKey(prefs["session"], "restore_on_startup", float64(4))
	out, err := json.Marshal(prefs)
	if err != nil {
		return
	}
	// Writing the prefs is best effort; a failure only loses the customization.
	_ = os.WriteFile(prefsPath, out, 0o600)
}

func withKey(v any, key string, val any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		m = map[string]any{}
	}
	m[key] = val
	return m
}

// Launch starts Chromium and returns the ready CDP connection.
func Launch(ctx context.Context, opts LaunchOptions) (*Handle, error) {
	executable, err := ResolveExecutable(opts.Engine, opts.Executable)
	if err != nil {
		return nil, err
	}
	profile := paths.ProfileDir(opts.Session)
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return nil, err
	}
	ensureProfilePrefs(profile)

	cmd := exec.Command(executable, buildArgs(opts, profile)...)
	// Its own process group, so the whole browser can be terminated at once.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", executable, err)
	}

	wsURL, err := waitForDevTools(stderr, cmd, 30*time.Second)
	if err != nil {
		return nil, err
	}

	client, err := cdp.Dial(ctx, wsURL, 15*time.Second)
	if err != nil {
		// Best effort: the process may already be gone.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return nil, err
	}
	return &Handle{
		Client:     client,
		WSURL:      wsURL,
		Executable: executable,
		ProfileDir: profile,
		Cmd:        cmd,
	}, nil
}

func waitForDevTools(stderr io.ReadCloser, cmd *exec.Cmd, timeout time.Duration) (string, error) {
	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)

	var mu sync.Mutex
	var tail strings.Builder

	// The end of stderr signals that the process died, so cmd.Wait() need not run
	// in parallel with the reading (which would close the pipe).
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			mu.Lock()
			if tail.Len() < 4000 {
				tail.WriteString(line)
				tail.WriteString("\n")
			}
			mu.Unlock()
			if m := devToolsRe.FindStringSubmatch(line); m != nil {
				urlCh <- m[1]
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
			return
		}
		errCh <- fmt.Errorf("stderr closed without announcing DevTools")
	}()

	snapshotTail := func() string {
		mu.Lock()
		defer mu.Unlock()
		return tail.String()
	}

	select {
	case url := <-urlCh:
		go func() { _ = cmd.Wait() }() // reap when the browser exits
		return url, nil
	case err := <-errCh:
		// Best effort: the real error is returned below.
		_ = cmd.Wait()
		return "", fmt.Errorf("%v. stderr:\n%s", err, snapshotTail())
	case <-time.After(timeout):
		// Best effort: the process is abandoned on timeout.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
		return "", fmt.Errorf("Chrome did not start within %s. stderr:\n%s", timeout, snapshotTail())
	}
}

// Attach connects to an already open Chromium (host:port or ws://...).
func Attach(ctx context.Context, target string) (*Handle, error) {
	wsURL := target
	if !strings.HasPrefix(target, "ws://") && !strings.HasPrefix(target, "wss://") {
		base := target
		if !strings.HasPrefix(base, "http") {
			base = "http://" + base
		}
		base = strings.TrimRight(base, "/")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json/version", nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("reading %s/json/version: %w", base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s/json/version returned %d", base, resp.StatusCode)
		}
		var info struct {
			WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return nil, err
		}
		if info.WebSocketDebuggerURL == "" {
			return nil, fmt.Errorf("no webSocketDebuggerUrl at %s", base)
		}
		wsURL = info.WebSocketDebuggerURL
	}
	client, err := cdp.Dial(ctx, wsURL, 15*time.Second)
	if err != nil {
		return nil, err
	}
	return &Handle{Client: client, WSURL: wsURL, Executable: "(attached)", Attached: true}, nil
}

// Exited reports whether the browser process started here has already died.
func (h *Handle) Exited() bool {
	return h.Cmd != nil && h.Cmd.ProcessState != nil && h.Cmd.ProcessState.Exited()
}

// Kill terminates the whole browser started here.
func (h *Handle) Kill() {
	if h.Cmd == nil || h.Cmd.Process == nil {
		return
	}
	// Best effort: the process may already be gone.
	_ = syscall.Kill(-h.Cmd.Process.Pid, syscall.SIGTERM)
}

// ProfilePath returns the default profile directory of a session.
func ProfilePath(session string) string {
	return filepath.Join(paths.StateDir(), "profiles", session)
}
