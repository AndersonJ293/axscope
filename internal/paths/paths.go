// Where the profile, socket and daemon info live. All outside the repository.
package paths

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// SessionInfo is what a live daemon writes about itself, so the CLI can list the
// sessions without depending on each daemon's command output.
type SessionInfo struct {
	Session string `json:"session"`
	Socket  string `json:"socket"`
	PID     int    `json:"pid"`
	Engine  string `json:"engine"`
}

// Sessions lists the sessions that have a daemon info file, sorted. A session
// whose daemon is already gone still appears here; the caller checks the socket.
func Sessions() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(StateDir(), "sessions"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(out)
	return out, nil
}

// ReadSessionInfo loads what the daemon wrote for a session.
func ReadSessionInfo(session string) (SessionInfo, error) {
	var info SessionInfo
	data, err := os.ReadFile(DaemonInfoPath(session))
	if err != nil {
		return info, err
	}
	return info, json.Unmarshal(data, &info)
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

// DownloadsDir is where `download` saves the files a page offers, one directory
// per session, so the path returned is stable and attachable.
func DownloadsDir(session string) string {
	return filepath.Join(StateDir(), "downloads", session)
}

// BrowserExecutableMarker points to the Chromium installed by `axscope install` for a product.
func BrowserExecutableMarker(product string) string {
	return filepath.Join(BrowsersDir(), "executable-"+product)
}
