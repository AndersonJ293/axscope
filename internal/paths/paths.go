// Onde ficam perfil, socket e info do daemon. Tudo fora do repositório.
package paths

import (
	"os"
	"path/filepath"
)

// StateDir guarda o que é persistente (perfis, logs, binário baixado).
func StateDir() string {
	if v := os.Getenv("BROWSER_USE_HOME"); v != "" {
		return v
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "browser-use")
}

// RuntimeDir guarda o que é efêmero (o socket), no runtime do usuário.
func RuntimeDir() string {
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "browser-use")
	}
	return filepath.Join(os.TempDir(), "browser-use")
}

// Session é o nome do conjunto de abas/daemon. Permite várias sessões paralelas.
func Session() string {
	if v := os.Getenv("BROWSER_USE_SESSION"); v != "" {
		return v
	}
	return "default"
}

func ProfileDir(session string) string {
	return filepath.Join(StateDir(), "profiles", session)
}

func SocketPath(session string) string {
	return filepath.Join(RuntimeDir(), session+".sock")
}

func DaemonInfoPath(session string) string {
	return filepath.Join(StateDir(), "sessions", session+".json")
}

// ActiveTabPath lembra qual aba estava ativa, para reiniciar o daemon não
// trocar a aba por baixo do agente.
func ActiveTabPath(session string) string {
	return filepath.Join(StateDir(), "sessions", session+".active")
}

func DaemonLogPath(session string) string {
	return filepath.Join(StateDir(), "logs", session+".log")
}

func BrowsersDir() string {
	return filepath.Join(StateDir(), "browsers")
}

// BrowserExecutableMarker aponta para o Chromium baixado por `bu install`,
// por produto (chrome ou chrome-headless-shell).
func BrowserExecutableMarker(product string) string {
	return filepath.Join(BrowsersDir(), "executable-"+product)
}
