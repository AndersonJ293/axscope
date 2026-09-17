// Download: trigger a file the page offers and wait for it to land.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// Download points the browser's downloads at `dir`, triggers the target with a
// real click and waits for the file to land, returning its path. Chrome writes
// `<name>.crdownload` first and renames it when it finishes, so a finished file
// is one without that suffix whose size has settled.
//
// When the browser refuses to be told where to save — extension mode, where
// chrome.debugger blocks the download commands — it falls back to the browser's
// own download directory.
func Download(ctx context.Context, client *cdp.Client, session string, t *Target, dir string, timeout time.Duration, p Presenter) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := setDownloadDir(ctx, client, session, dir); err != nil {
		dir = defaultDownloadDir()
	}
	before, err := dirFiles(dir)
	if err != nil {
		return "", err
	}
	// The click may answer with a "did not reach" warning while the file still
	// lands, so the wait below is what decides.
	if _, err := Click(ctx, client, session, t, "left", 1, p); err != nil {
		return "", err
	}
	return waitForDownload(dir, before, timeout)
}

// defaultDownloadDir is where the browser saves a download it was not told about:
// the XDG download directory when it is set, `~/Downloads` otherwise.
func defaultDownloadDir() string {
	if out, err := exec.Command("xdg-user-dir", "DOWNLOAD").Output(); err == nil {
		if dir := strings.TrimSpace(string(out)); dir != "" {
			return dir
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Downloads")
	}
	return os.TempDir()
}

// Href returns the absolute URL a target points at (`this.href`), or "" when it is
// not a link. A link can be fetched by the browser itself, which the extension
// saves without the save dialog the debugger cannot suppress.
func Href(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": `function () { return (this && typeof this.href === 'string') ? this.href : ''; }`,
		"returnByValue":       true,
	}, session)
	if err != nil {
		return ""
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.Result.Value
}

// DownloadURL asks the extension to save a URL with chrome.downloads and waits
// for the path. The browser fetches it, so it carries the session's cookies; the
// extension uses saveAs:false, which does not prompt.
func DownloadURL(ctx context.Context, client *cdp.Client, session, url string, timeout time.Duration) (string, error) {
	raw, err := client.Send(ctx, "Axscope.download", map[string]any{"url": url}, session)
	if err != nil {
		return "", err
	}
	var id int
	if json.Unmarshal(raw, &id) != nil {
		return "", fmt.Errorf("the extension did not start the download")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := client.Send(ctx, "Axscope.downloadStatus", map[string]any{"id": id}, session)
		if err != nil {
			return "", err
		}
		var item *struct {
			State    string `json:"state"`
			Filename string `json:"filename"`
			Error    string `json:"error"`
		}
		if json.Unmarshal(raw, &item) == nil && item != nil {
			switch item.State {
			case "complete":
				return item.Filename, nil
			case "interrupted":
				return "", fmt.Errorf("the download was interrupted: %s", item.Error)
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return "", fmt.Errorf("the download did not finish within %s", timeout)
}

// setDownloadDir tells the browser to save downloads in `dir` without prompting.
// Page.setDownloadBehavior is tab-scoped; Browser.setDownloadBehavior is the
// fallback. Both go through the session, because a browser-level command without
// a tab is refused when a debugger is attached through the extension.
func setDownloadDir(ctx context.Context, client *cdp.Client, session, dir string) error {
	pageErr := error(nil)
	if _, err := client.Send(ctx, "Page.setDownloadBehavior", map[string]any{
		"behavior":     "allow",
		"downloadPath": dir,
	}, session); err == nil {
		return nil
	} else {
		pageErr = err
	}
	if _, err := client.Send(ctx, "Browser.setDownloadBehavior", map[string]any{
		"behavior":     "allow",
		"downloadPath": dir,
	}, session); err != nil {
		return fmt.Errorf("%w (Page.setDownloadBehavior also failed: %v)", err, pageErr)
	}
	return nil
}

// dirFiles lists the regular file names of a directory.
func dirFiles(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names[e.Name()] = true
		}
	}
	return names, nil
}

// isPartialDownload reports the names a browser uses while a file is still being
// written: `.crdownload`, and Chromium's hidden `.org.chromium.Chromium.*` temp
// that is renamed to the real name when the download finishes.
func isPartialDownload(name string) bool {
	return strings.HasSuffix(name, ".crdownload") || strings.HasPrefix(name, ".org.chromium.Chromium.")
}

// waitForDownload waits for a new file to appear and stop growing. A file that
// started but never finished is named in the refusal.
func waitForDownload(dir string, before map[string]bool, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var last string
	var lastSize int64
	stable := 0
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || before[name] || isPartialDownload(name) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(dir, name)
			if path == last && info.Size() == lastSize {
				// Two polls with the same size: the rename happened and the
				// writer is done.
				stable++
				if stable >= 2 {
					return path, nil
				}
			} else {
				last, lastSize, stable = path, info.Size(), 0
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if last != "" {
		return "", fmt.Errorf("the download started (%s) but did not finish within %s", last, timeout)
	}
	return "", fmt.Errorf("no file was downloaded to %s — does the target offer a download?", dir)
}
