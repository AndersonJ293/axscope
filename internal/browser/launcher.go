// Encontra, sobe e conecta ao Chromium.
//
// Duas formas de entrar:
//  1. Launch: sobe um Chrome for Testing (ou o do sistema) com perfil próprio e
//     persistente, headed por padrão, e lê a URL CDP do stderr.
//  2. Attach: conecta a um Chromium já aberto com --remote-debugging-port.
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

	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/paths"
)

var devToolsRe = regexp.MustCompile(`DevTools listening on (ws://\S+)`)

// Engines suportados.
const (
	// EngineChrome é o Chrome for Testing: janela de verdade, cursor visível.
	EngineChrome = "chrome"
	// EngineShell é o chrome-headless-shell: o mesmo motor e o mesmo CDP,
	// sem janela — mais leve, mas sem cursor renderizado.
	EngineShell = "shell"
	// EngineExt dirige o navegador do usuário (Brave) pela extensão, via
	// chrome.debugger: perfil real, com os logins já feitos.
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

// LaunchOptions descreve como subir o browser.
type LaunchOptions struct {
	Session    string
	Engine     string
	Headless   bool
	Executable string
	WindowSize string
	ExtraArgs  []string
	// Porta fixa de debug; 0 escolhe uma livre (lida do stderr).
	Port int
}

// Handle é um browser pronto para uso.
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

// ResolveExecutable acha o Chromium: flag, env, baixado por `bu install`, sistema.
func ResolveExecutable(engine, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv("BROWSER_USE_CHROME"); v != "" {
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
		return "", fmt.Errorf("chrome-headless-shell não instalado: rode `browser-use install --engine shell`")
	}
	for _, name := range systemCandidates {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("nenhum Chromium encontrado: rode `browser-use install` ou defina BROWSER_USE_CHROME")
}

// Engine é um motor disponível no sistema.
type Engine struct {
	Product string
	Path    string
	Exists  bool
}

// DetectEngines lista os motores instalados (baixados e do sistema).
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
	// chrome-headless-shell já é headless: não recebe --headless=new.
	if opts.Headless && opts.Engine != EngineShell {
		args = append(args, "--headless=new")
	}
	if opts.Headless || opts.Engine == EngineShell {
		args = append(args, "--disable-gpu")
	}
	// A árvore de acessibilidade é ligada sob demanda pelo domínio Accessibility.
	// Forçá-la no arranque custa memória em todo processo; só ligamos se pedido.
	if envBool("BROWSER_USE_FORCE_AX", false) {
		args = append(args, "--force-renderer-accessibility")
	}
	args = append(args, opts.ExtraArgs...)
	return args
}

// ensureProfilePrefs deixa o arranque determinístico: sem restaurar as abas de
// rodadas anteriores, que só poluem a janela de quem está olhando.
func ensureProfilePrefs(profile string) {
	prefsPath := filepath.Join(profile, "Default", "Preferences")
	if err := os.MkdirAll(filepath.Dir(prefsPath), 0o755); err != nil {
		return
	}
	prefs := map[string]any{}
	if data, err := os.ReadFile(prefsPath); err == nil {
		if err := json.Unmarshal(data, &prefs); err != nil {
			return // não mexe em JSON que não entendemos
		}
	}
	prefs["profile"] = withKey(prefs["profile"], "exit_type", "Normal")
	prefs["session"] = withKey(prefs["session"], "restore_on_startup", float64(4))
	out, err := json.Marshal(prefs)
	if err != nil {
		return
	}
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

// Launch sobe o Chromium e devolve a conexão CDP pronta.
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
	// Grupo próprio de processos: dá para encerrar o browser inteiro de uma vez.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("subindo %s: %w", executable, err)
	}

	wsURL, err := waitForDevTools(stderr, cmd, 30*time.Second)
	if err != nil {
		return nil, err
	}

	client, err := cdp.Dial(ctx, wsURL, 15*time.Second)
	if err != nil {
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

	// O fim do stderr é o sinal de que o processo morreu: assim não precisamos
	// chamar cmd.Wait() em paralelo com a leitura (o que fecharia o pipe).
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
		errCh <- fmt.Errorf("stderr fechou sem anunciar o DevTools")
	}()

	snapshotTail := func() string {
		mu.Lock()
		defer mu.Unlock()
		return tail.String()
	}

	select {
	case url := <-urlCh:
		go func() { _ = cmd.Wait() }() // reap quando o browser sair
		return url, nil
	case err := <-errCh:
		_ = cmd.Wait()
		return "", fmt.Errorf("%v. stderr:\n%s", err, snapshotTail())
	case <-time.After(timeout):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
		return "", fmt.Errorf("Chrome não subiu em %s. stderr:\n%s", timeout, snapshotTail())
	}
}

// Attach conecta a um Chromium já aberto (host:port ou ws://...).
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
			return nil, fmt.Errorf("lendo %s/json/version: %w", base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s/json/version devolveu %d", base, resp.StatusCode)
		}
		var info struct {
			WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return nil, err
		}
		if info.WebSocketDebuggerURL == "" {
			return nil, fmt.Errorf("sem webSocketDebuggerUrl em %s", base)
		}
		wsURL = info.WebSocketDebuggerURL
	}
	client, err := cdp.Dial(ctx, wsURL, 15*time.Second)
	if err != nil {
		return nil, err
	}
	return &Handle{Client: client, WSURL: wsURL, Executable: "(anexado)", Attached: true}, nil
}

// Exited informa se o processo do browser que subimos já morreu.
func (h *Handle) Exited() bool {
	return h.Cmd != nil && h.Cmd.ProcessState != nil && h.Cmd.ProcessState.Exited()
}

// Kill encerra o browser inteiro (só quando fomos nós que subimos).
func (h *Handle) Kill() {
	if h.Cmd == nil || h.Cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-h.Cmd.Process.Pid, syscall.SIGTERM)
}

// ProfilePath devolve o diretório de perfil padrão de uma sessão.
func ProfilePath(session string) string {
	return filepath.Join(paths.StateDir(), "profiles", session)
}
