package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AndersonJ293/axscope/internal/paths"
)

// runClean deletes what is disposable. Without --all, it preserves the profile
// (logins) and the downloaded browsers, which are what takes work to redo.
func runClean(args []string) error {
	all := false
	for _, a := range args {
		if a == "--all" || a == "all" {
			all = true
		}
	}

	targets := []string{filepath.Join(paths.StateDir(), "logs")}
	if all {
		targets = append(targets,
			filepath.Join(paths.StateDir(), "profiles"),
			filepath.Join(paths.StateDir(), "browsers"),
		)
	}

	var freed int64
	for _, dir := range targets {
		size := dirSize(dir)
		if size == 0 {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "could not remove %s: %v\n", dir, err)
			continue
		}
		freed += size
		fmt.Printf("removed %-24s %s\n", filepath.Base(dir), human(size))
	}

	// Sessions whose socket no longer exists are garbage.
	sessionsDir := filepath.Join(paths.StateDir(), "sessions")
	if entries, err := os.ReadDir(sessionsDir); err == nil {
		for _, e := range entries {
			session := strings.TrimSuffix(e.Name(), ".json")
			if _, err := os.Stat(paths.SocketPath(session)); err != nil {
				_ = os.Remove(filepath.Join(sessionsDir, e.Name()))
				fmt.Printf("removed session %q (no daemon)\n", session)
			}
		}
	}

	if freed == 0 {
		fmt.Println("nothing to clean")
	} else {
		fmt.Printf("freed: %s\n", human(freed))
	}
	return nil
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func human(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
