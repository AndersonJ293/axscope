package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// StateDir is the persistent root: AXSCOPE_HOME wins, then XDG_DATA_HOME, then
// ~/.local/share. An empty variable means "not set" for every override.
func TestStateDir(t *testing.T) {
	cases := []struct {
		name    string
		home    string
		xdgData string
		want    string
	}{
		{"AXSCOPE_HOME wins", "/tmp/axscope-home", "/tmp/xdg-data", "/tmp/axscope-home"},
		{"XDG_DATA_HOME when AXSCOPE_HOME is empty", "", "/tmp/xdg-data", filepath.Join("/tmp/xdg-data", "axscope")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("AXSCOPE_HOME", c.home)
			t.Setenv("XDG_DATA_HOME", c.xdgData)
			if got := StateDir(); got != c.want {
				t.Errorf("StateDir() = %q, want %q", got, c.want)
			}
		})
	}
}

// With no override at all the data root is the home fallback; the expectation is
// computed from the same lookup so the test does not pin a machine-specific path.
func TestStateDirFallsBackToHome(t *testing.T) {
	t.Setenv("AXSCOPE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	want := filepath.Join(home, ".local", "share", "axscope")
	if got := StateDir(); got != want {
		t.Errorf("StateDir() = %q, want %q", got, want)
	}
}

// RuntimeDir is ephemeral and lives under XDG_RUNTIME_DIR, falling back to the
// temp directory when there is no runtime dir.
func TestRuntimeDir(t *testing.T) {
	cases := []struct {
		name    string
		runtime string
		want    string
	}{
		{"XDG_RUNTIME_DIR", "/run/user/1000", filepath.Join("/run/user/1000", "axscope")},
		{"falls back to a per-user temp dir", "", filepath.Join(os.TempDir(), fmt.Sprintf("axscope-%d", os.Getuid()))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", c.runtime)
			if got := RuntimeDir(); got != c.want {
				t.Errorf("RuntimeDir() = %q, want %q", got, c.want)
			}
		})
	}
}

// Session names the tab/daemon set; the default keeps the common case stable.
func TestSession(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"explicit", "work", "work"},
		{"unset falls back to default", "", "default"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("AXSCOPE_SESSION", c.env)
			if got := Session(); got != c.want {
				t.Errorf("Session() = %q, want %q", got, c.want)
			}
		})
	}
}

// ProfileDir and SocketPath derive from StateDir/RuntimeDir plus the session, so
// a change in the roots moves every derived path together.
func TestDerivedPaths(t *testing.T) {
	t.Setenv("AXSCOPE_HOME", "/tmp/axscope-home")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	if got := ProfileDir("work"); got != filepath.Join("/tmp/axscope-home", "profiles", "work") {
		t.Errorf("ProfileDir = %q", got)
	}
	if got := SocketPath("work"); got != filepath.Join("/run/user/1000", "axscope", "work.sock") {
		t.Errorf("SocketPath = %q", got)
	}
	if got := DaemonInfoPath("work"); got != filepath.Join("/tmp/axscope-home", "sessions", "work.json") {
		t.Errorf("DaemonInfoPath = %q", got)
	}
	if got := ActiveTabPath("work"); got != filepath.Join("/tmp/axscope-home", "sessions", "work.active") {
		t.Errorf("ActiveTabPath = %q", got)
	}
	if got := DaemonLogPath("work"); got != filepath.Join("/tmp/axscope-home", "logs", "work.log") {
		t.Errorf("DaemonLogPath = %q", got)
	}
	if got := BrowsersDir(); got != filepath.Join("/tmp/axscope-home", "browsers") {
		t.Errorf("BrowsersDir = %q", got)
	}
	if got := BrowserExecutableMarker("chrome"); got != filepath.Join("/tmp/axscope-home", "browsers", "executable-chrome") {
		t.Errorf("BrowserExecutableMarker = %q", got)
	}

	// The session override flows into the derived paths.
	t.Setenv("AXSCOPE_SESSION", "work")
	if got := ProfileDir(Session()); got != ProfileDir("work") {
		t.Errorf("ProfileDir(Session()) = %q, want %q", got, ProfileDir("work"))
	}
	if got := SocketPath(Session()); got != filepath.Join("/run/user/1000", "axscope", "work.sock") {
		t.Errorf("SocketPath(Session()) = %q", got)
	}
}
