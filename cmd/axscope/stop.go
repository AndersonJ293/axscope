package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AndersonJ293/axscope/internal/daemonclient"
	"github.com/AndersonJ293/axscope/internal/paths"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// runStopAll stops all live daemons (and the browsers they started).
func runStopAll() error {
	dir := filepath.Join(paths.StateDir(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Println("no active sessions")
		return nil
	}
	stopped := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		session := strings.TrimSuffix(e.Name(), ".json")
		socketPath := paths.SocketPath(session)
		if _, err := os.Stat(socketPath); err != nil {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		if _, err := daemonclient.SendTo(socketPath, protocol.Request{Cmd: "stop"}); err == nil {
			fmt.Printf("stopped session %q\n", session)
			stopped++
		}
	}
	if stopped == 0 {
		fmt.Println("no active sessions")
	}
	return nil
}
