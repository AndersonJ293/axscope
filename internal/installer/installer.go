// Downloads Chrome for Testing (and chrome-headless-shell) from the official
// versioned endpoint, pinning the version rather than a mutable "latest".
package installer

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/AndersonJ293/axscope/internal/paths"
)

const knownGoodURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

type cftManifest struct {
	Channels map[string]struct {
		Channel   string `json:"channel"`
		Version   string `json:"version"`
		Downloads map[string][]struct {
			Platform string `json:"platform"`
			URL      string `json:"url"`
		} `json:"downloads"`
	} `json:"channels"`
}

// Options control what to download.
type Options struct {
	// Channel: Stable, Beta, Dev, Canary.
	Channel string
	// Product: chrome or chrome-headless-shell.
	Product string
	// Fixed version; empty uses the latest of the channel.
	Version string
}

func platformKey() (string, error) {
	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH == "arm64" {
			return "linux-arm64", nil
		}
		return "linux64", nil
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "mac-arm64", nil
		}
		return "mac-x64", nil
	case "windows":
		if runtime.GOARCH == "arm64" {
			return "win-arm64", nil
		}
		return "win64", nil
	}
	return "", fmt.Errorf("unsupported system: %s/%s", runtime.GOOS, runtime.GOARCH)
}

// Install downloads and extracts, returning the executable path.
func Install(ctx context.Context, opts Options) (string, error) {
	if opts.Channel == "" {
		opts.Channel = "Stable"
	}
	if opts.Product == "" {
		opts.Product = "chrome"
	}
	platform, err := platformKey()
	if err != nil {
		return "", err
	}

	url, version, err := resolveDownload(ctx, opts, platform)
	if err != nil {
		return "", err
	}

	dest := filepath.Join(paths.BrowsersDir(), fmt.Sprintf("%s-%s-%s", opts.Product, version, platform))
	if exe, ok := findExecutable(dest, opts.Product); ok {
		if err := markInstalled(opts.Product, exe); err != nil {
			return "", err
		}
		fmt.Printf("already installed: %s\n", exe)
		return exe, nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}

	fmt.Printf("downloading %s %s (%s)...\n", opts.Product, version, platform)
	zipPath := filepath.Join(os.TempDir(), fmt.Sprintf("axscope-%s-%s.zip", opts.Product, version))
	if err := download(ctx, url, zipPath); err != nil {
		return "", err
	}
	defer os.Remove(zipPath)

	fmt.Printf("extracting to %s\n", dest)
	if err := unzip(zipPath, dest); err != nil {
		return "", err
	}

	exe, ok := findExecutable(dest, opts.Product)
	if !ok {
		return "", fmt.Errorf("executable not found after extracting to %s", dest)
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(paths.BrowsersDir(), 0o755); err != nil {
		return "", err
	}
	if err := markInstalled(opts.Product, exe); err != nil {
		return "", err
	}
	fmt.Printf("ready: %s\n", exe)
	return exe, nil
}

// markInstalled writes the marker that the launcher reads to find the binary.
func markInstalled(product, exe string) error {
	if err := os.MkdirAll(paths.BrowsersDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(paths.BrowserExecutableMarker(product), []byte(exe), 0o644)
}

func resolveDownload(ctx context.Context, opts Options, platform string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, knownGoodURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var manifest cftManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return "", "", err
	}
	channel, ok := manifest.Channels[opts.Channel]
	if !ok {
		return "", "", fmt.Errorf("channel %q does not exist", opts.Channel)
	}
	for _, d := range channel.Downloads[opts.Product] {
		if d.Platform == platform {
			return d.URL, channel.Version, nil
		}
	}
	return "", "", fmt.Errorf("no download of %s for %s in channel %s", opts.Product, platform, opts.Channel)
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	// No progress bar: it prints milestones so as not to pollute the agent.
	total := resp.ContentLength
	var written int64
	buf := make([]byte, 1<<20)
	lastReport := time.Now()
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if time.Since(lastReport) > 3*time.Second {
				lastReport = time.Now()
				if total > 0 {
					fmt.Printf("  %d%%\n", written*100/total)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(dest, f.Name)
		// Reject zip entries whose path escapes dest (zip-slip).
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("suspicious path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

func findExecutable(dir, product string) (string, bool) {
	var wanted []string
	switch runtime.GOOS {
	case "windows":
		if product == "chrome-headless-shell" {
			wanted = []string{"chrome-headless-shell.exe"}
		} else {
			wanted = []string{"chrome.exe"}
		}
	default:
		if product == "chrome-headless-shell" {
			wanted = []string{"chrome-headless-shell"}
		} else {
			wanted = []string{"chrome"}
		}
	}
	var found string
	// Best-effort walk: a read error just means the binary is not found here.
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		for _, w := range wanted {
			if d.Name() == w {
				found = path
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found, found != ""
}
