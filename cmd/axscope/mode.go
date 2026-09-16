package main

import (
	"os"

	"github.com/AndersonJ293/axscope/internal/browser"
)

// applyMode sets the engine (and the session, so the modes coexist).
func applyMode(mode string) {
	switch mode {
	case "chrome":
		_ = os.Setenv("AXSCOPE_ENGINE", browser.EngineChrome)
		if os.Getenv("AXSCOPE_SESSION") == "" {
			_ = os.Setenv("AXSCOPE_SESSION", "chrome")
		}
	case "ext":
		_ = os.Setenv("AXSCOPE_ENGINE", browser.EngineExt)
		if os.Getenv("AXSCOPE_SESSION") == "" {
			_ = os.Setenv("AXSCOPE_SESSION", "ext")
		}
	case "headless":
		_ = os.Setenv("AXSCOPE_ENGINE", browser.EngineShell)
		if os.Getenv("AXSCOPE_SESSION") == "" {
			_ = os.Setenv("AXSCOPE_SESSION", "headless")
		}
	}
}
