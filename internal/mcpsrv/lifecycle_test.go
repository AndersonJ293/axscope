package mcpsrv

import (
	"os"
	"testing"
	"time"
)

// The watchdog cuts only a server nobody can use: a dead client at once, a
// superseded one only when idle and with nothing in flight.
func TestShouldExit(t *testing.T) {
	cases := []struct {
		name       string
		nowPPID    int
		superseded bool
		idle       time.Duration
		inFlight   int
		want       bool
	}{
		{"healthy", 100, false, time.Hour, 0, false},
		{"client gone", 1, false, 0, 1, true},
		{"superseded but busy", 100, true, time.Hour, 1, false},
		{"superseded but recently used", 100, true, time.Minute, 0, false},
		{"superseded and idle", 100, true, supersededIdle, 0, true},
		{"idle alone is fine", 100, false, 24 * time.Hour, 0, false},
	}
	for _, c := range cases {
		if got, _ := shouldExit(100, c.nowPPID, c.superseded, c.idle, c.inFlight); got != c.want {
			t.Errorf("%s: shouldExit = %v, want %v", c.name, got, c.want)
		}
	}
}

// The newest record names one live server per parent; a server is superseded
// only by another live one, never by itself or by a dead pid.
func TestNewestRecord(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	const ppid = 424242
	claimNewest(ppid)
	if superseded(ppid) {
		t.Error("a server is not superseded by its own record")
	}
	// Another live process (the test's parent) claims the parent.
	if err := os.WriteFile(newestPath(ppid), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !superseded(ppid) {
		t.Error("a newer live server must supersede this one")
	}
	if err := os.WriteFile(newestPath(ppid), []byte("999999999"), 0o600); err != nil {
		t.Fatal(err)
	}
	if superseded(ppid) {
		t.Error("a dead pid must not supersede anyone")
	}
	releaseNewest(ppid) // not ours: stays
	if _, err := os.Stat(newestPath(ppid)); err != nil {
		t.Error("release removed another server's record")
	}
	claimNewest(ppid)
	releaseNewest(ppid)
	if _, err := os.Stat(newestPath(ppid)); !os.IsNotExist(err) {
		t.Error("release kept this server's own record")
	}
}

// A client that runs one server per project directory under one process
// (opencode) must not have its servers read as replacements of each other.
func TestNewestIsPerDirectory(t *testing.T) {
	here := newestPath(4242)
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if newestPath(4242) == here {
		t.Error("two directories share the newest record")
	}
	t.Setenv("AXSCOPE_SESSION", "other")
	if newestPath(4242) == here {
		t.Error("two sessions share the newest record")
	}
}
