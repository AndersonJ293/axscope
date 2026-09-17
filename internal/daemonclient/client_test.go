package daemonclient

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/AndersonJ293/axscope/internal/protocol"
)

// Regression: the read used to have no deadline, so a daemon that accepted the
// request and never answered blocked the client forever — the hang an agent
// showed as a call that never returned. The context must end the wait.
func TestSendToContextReturnsOnDeadline(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "hang.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	t.Cleanup(func() {
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := SendToContext(ctx, sock, protocol.Request{Cmd: "ping"}); err == nil {
		t.Fatal("a daemon that never answers must surface an error, not hang")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the wait outlived the deadline: %s", elapsed)
	}
}
