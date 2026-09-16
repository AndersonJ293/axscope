// Where the profile, socket and daemon info live. All outside the repository.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// StateDir stores what is persistent (profiles, logs, downloaded binary).
func StateDir() string {
	if v := os.Getenv("AXSCOPE_HOME"); v != "" {
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
	return filepath.Join(base, "axscope")
}

// RuntimeDir stores what is ephemeral (the socket), in the user's runtime.
func RuntimeDir() string {
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "axscope")
	}
	// The temp fallback is shared and predictable, so the directory is named
	// after the user: another local user cannot own the path we use.
	return filepath.Join(os.TempDir(), fmt.Sprintf("axscope-%d", os.Getuid()))
}

// Session is the name of the tab/daemon set. It allows multiple parallel sessions.
func Session() string {
	if v := os.Getenv("AXSCOPE_SESSION"); v != "" {
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

// ActiveTabPath remembers the active tab across daemon restarts so it does not switch under the agent.
func ActiveTabPath(session string) string {
	return filepath.Join(StateDir(), "sessions", session+".active")
}

func DaemonLogPath(session string) string {
	return filepath.Join(StateDir(), "logs", session+".log")
}

func BrowsersDir() string {
	return filepath.Join(StateDir(), "browsers")
}

// BrowserExecutableMarker points to the Chromium installed by `axscope install` for a product.
func BrowserExecutableMarker(product string) string {
	return filepath.Join(BrowsersDir(), "executable-"+product)
}
