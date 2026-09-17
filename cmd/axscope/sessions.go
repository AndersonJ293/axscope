package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/daemonclient"
	"github.com/AndersonJ293/axscope/internal/paths"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// runSessions lists the sessions whose daemon is still answering, so a human can
// attach to one the MCP auto-provisioned.
func runSessions() error {
	sessions, err := paths.Sessions()
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(sessions))
	for _, session := range sessions {
		info, err := paths.ReadSessionInfo(session)
		if err != nil {
			continue
		}
		if _, err := os.Stat(info.Socket); err != nil {
			continue // the daemon is gone; `clean` removes the leftover file
		}
		tabs := "-"
		if resp, err := daemonclient.SendTo(info.Socket, protocol.Request{Cmd: "status"}); err == nil && resp.OK {
			if n := statusField(resp.Text, "tabs"); n != "" {
				tabs = n
			}
		}
		rows = append(rows, []string{session, info.Engine, strconv.Itoa(info.PID), tabs})
	}
	if len(rows) == 0 {
		fmt.Println("no live sessions")
		return nil
	}
	fmt.Printf("%-20s %-10s %-8s %s\n", "SESSION", "ENGINE", "PID", "TABS")
	for _, r := range rows {
		fmt.Printf("%-20s %-10s %-8s %s\n", r[0], r[1], r[2], r[3])
	}
	fmt.Println()
	fmt.Println("attach with: AXSCOPE_SESSION=<session> axscope <command>")
	return nil
}

// statusField pulls one `name: value` line out of `status` output.
func statusField(text, name string) string {
	prefix := name + ": "
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}
