package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestListenUnixIsOwnerOnly guards the fix for the daemon socket being
// world-accessible on the temp-dir fallback.
func TestListenUnixIsOwnerOnly(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "session.sock")

	ln, err := listenUnix(socketPath)
	if err != nil {
		t.Fatalf("listenUnix: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("path is not a socket: %v", info.Mode())
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %04o, want 0600", perm)
	}
}

// TestEnsureRuntimeDirCreatesOwnerOnly guards the temp-dir fallback: the
// directory that holds the socket must be created owner-only.
func TestEnsureRuntimeDirCreatesOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "axscope-test")

	if err := ensureRuntimeDir(dir); err != nil {
		t.Fatalf("ensureRuntimeDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %04o, want 0700", perm)
	}
}

// TestEnsureRuntimeDirTightensExistingDir handles a directory left behind with
// looser permissions.
func TestEnsureRuntimeDirTightensExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "axscope-test")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := ensureRuntimeDir(dir); err != nil {
		t.Fatalf("ensureRuntimeDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %04o, want 0700", perm)
	}
}

// TestEnsureRuntimeDirRejectsSymlink refuses a runtime path another user could
// have pointed elsewhere.
func TestEnsureRuntimeDirRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := ensureRuntimeDir(link); err == nil {
		t.Error("symlink runtime directory accepted, want refusal")
	}
}

// TestEnsureRuntimeDirRejectsFile refuses a non-directory at the runtime path.
func TestEnsureRuntimeDirRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ensureRuntimeDir(path); err == nil {
		t.Error("regular file accepted as runtime directory, want refusal")
	}
}

// TestRemoveStaleSocket keeps a live socket and anything that is not a socket,
// and removes only a socket left behind by a dead daemon.
func TestRemoveStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dead.sock")

	removeStaleSocket(path) // nothing there: no-op

	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	removeStaleSocket(path)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("regular file was removed: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	ln, err := listenUnix(path)
	if err != nil {
		t.Fatalf("listenUnix: %v", err)
	}
	removeStaleSocket(path)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("live socket was removed: %v", err)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	removeStaleSocket(path)
	if _, err := os.Stat(path); err == nil {
		t.Error("stale socket was kept")
	}
}
