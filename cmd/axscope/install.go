package main

import (
	"context"

	"github.com/AndersonJ293/axscope/internal/installer"
)

// runInstall downloads the requested engines; `--engine` accepts chrome, shell or all.
func runInstall(args []string) error {
	engine := "chrome"
	for i, a := range args {
		if a == "--engine" && i+1 < len(args) {
			engine = args[i+1]
		} else if a == "shell" || a == "headless" {
			engine = "shell"
		}
	}
	var products []string
	switch engine {
	case "shell", "headless", "chrome-headless-shell":
		products = []string{"chrome-headless-shell"}
	case "all":
		products = []string{"chrome", "chrome-headless-shell"}
	default:
		products = []string{"chrome"}
	}
	for _, product := range products {
		if _, err := installer.Install(context.Background(), installer.Options{Product: product}); err != nil {
			return err
		}
	}
	return nil
}
