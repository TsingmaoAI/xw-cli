package app

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// PullOptions holds options for the pull command
type PullOptions struct {
	*GlobalOptions

	// Model is the model name to pull
	Model string
}

// NewPullCommand creates the pull command.
//
// The pull command downloads and installs an AI model from the registry,
// making it available for execution.
//
// Usage:
//
//	xw pull MODEL
//
// Examples:
//
//	xw pull qwen2-0.5b
//	xw pull qwen2-7b
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for pulling models
func NewPullCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &PullOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "pull MODEL",
		Short: "Download a model",
		Long: `Download and install an AI model.

The model files are downloaded to the xw server and prepared for execution.
This command must be run before a model can be used with 'xw run'.`,
		Example: `  xw pull qwen2-0.5b
  xw pull qwen2-7b`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Model = args[0]
			return runPull(opts)
		},
	}

	return cmd
}

// runPull executes the pull command logic.
//
// This function sends the model pull request to the server and displays
// progress information.
//
// Parameters:
//   - opts: Pull command options
//
// Returns:
//   - nil on success
//   - error if the request fails or the model doesn't exist
func runPull(opts *PullOptions) error {
	client := getClient(opts.GlobalOptions)

	fmt.Printf("Pulling %s...\n", opts.Model)

	// Pull model with single-line progress display
	resp, err := client.Pull(opts.Model, "", func(message string) {
		// Only show progress bar (contains % and |)
		if strings.Contains(message, "%") && strings.Contains(message, "|") {
			// Use \r to overwrite, \033[K to clear to end of line
			fmt.Printf("\r\033[K%s", message)
		}
		// Silently ignore all other messages
	})
	
	// Move to newline when done
	fmt.Println()
	
	if err != nil {
		return fmt.Errorf("failed to pull model: %w", err)
	}

	// Display final result
	if resp.Status == "success" {
		fmt.Printf("✓ %s\n", resp.Message)
	} else {
		fmt.Printf("Status: %s\n", resp.Status)
		if resp.Message != "" {
			fmt.Printf("Message: %s\n", resp.Message)
		}
	}

	return nil
}
