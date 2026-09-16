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
