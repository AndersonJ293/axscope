package mcpsrv

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/AndersonJ293/axscope/internal/paths"
)

// A client that reconnects its MCP servers may start a new axscope and leave the
// old one running with its stdin still open: the old server never reads EOF and
// lives on, with its auto session's browser, until the client itself exits. A
// real session left six of them behind. Two signals end it:
//
//   - the parent died (the server was reparented), so nobody can ever call it;
//   - a newer server of the same parent, in the same directory and session,
//     took over and this one sat idle for supersededIdle — an active server
//     is never cut. A client can run several on purpose: opencode starts one
//     per project directory under one process, and keying by the parent alone
//     cut all but the newest after ten idle minutes, which the client showed
//     as the server disconnecting over and over.
const (
	supersededIdle = 10 * time.Minute
	watchEvery     = 30 * time.Second
)

// lastCall is when this server last handled a request (unix nanos).
var lastCall atomic.Int64

func touch() { lastCall.Store(time.Now().UnixNano()) }

func idleFor() time.Duration { return time.Since(time.Unix(0, lastCall.Load())) }

// newestPath names the file where the newest server of a parent process — for
// this directory and session — writes its pid. A reconnect replaces a server
// with one just like it; a server for another directory or session is a
// sibling, not a replacement. It lives in the runtime dir: it means nothing
// after a reboot.
func newestPath(ppid int) string {
	return filepath.Join(paths.RuntimeDir(), "mcp", strconv.Itoa(ppid)+"-"+instanceKey()+".newest")
}

// instanceKey tells apart the servers one client runs side by side.
func instanceKey() string {
	cwd, _ := os.Getwd()
	h := fnv.New32a()
	h.Write([]byte(cwd + "\x00" + os.Getenv("AXSCOPE_SESSION")))
	return strconv.FormatUint(uint64(h.Sum32()), 16)
}

// claimNewest records this server as the newest of its parent.
func claimNewest(ppid int) {
	p := newestPath(ppid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp := p + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0o600) == nil {
		_ = os.Rename(tmp, p)
	}
}

// releaseNewest drops the record if it is still this server's, so a stale file
// does not outlive the parent's last server.
func releaseNewest(ppid int) {
	p := newestPath(ppid)
	if data, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(os.Getpid()) {
		_ = os.Remove(p)
	}
}

// superseded reports whether a newer, live server of the same parent exists.
func superseded(ppid int) bool {
	data, err := os.ReadFile(newestPath(ppid))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid == os.Getpid() {
		return false
	}
	return alive(pid)
}

// alive probes with signal 0; EPERM means the process exists but is not ours.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// shouldExit is the watchdog's decision, kept pure so it can be tested.
func shouldExit(startPPID, nowPPID int, isSuperseded bool, idle time.Duration, inFlight int) (bool, string) {
	if nowPPID != startPPID {
		return true, "the client process is gone"
	}
	if isSuperseded && idle >= supersededIdle && inFlight == 0 {
		return true, "a newer server of the same client took over"
	}
	return false, ""
}
