// Package main is the entry point for the xw CLI application.
//
// The xw CLI provides a command-line interface for interacting with the xw
// server, enabling users to manage and execute AI models on domestic chip
// architectures.
//
// Usage:
//
//	xw [command] [flags]
//
// Available commands:
//
//	ls      - List available models
//	run     - Execute a model
//	pull    - Download a model
//	version - Display version information
//	serve   - Start the xw server (for testing)
package main

import (
	"os"

	"github.com/tsingmao/xw/cmd/xw/app"
)

func main() {
	cmd := app.NewXWCommand()
	if err := cmd.Execute(); err != nil {
		// Error is already printed by cobra, just exit
		os.Exit(1)
	}
}
