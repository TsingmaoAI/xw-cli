package app

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsingmaoai/xw-cli/internal/api"
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

	// Check if model is supported by current device before pulling
	modelsResp, err := client.ListModelsWithStats(api.DeviceTypeAll, true)
	if err != nil {
		// If we can't get model list, just continue (backward compatibility)
		fmt.Printf("Warning: Unable to verify model compatibility: %v\n", err)
	} else {
		// Find the model in the list
		var modelFound bool
		var modelSupported bool
		for _, model := range modelsResp.Models {
			if model.Name == opts.Model {
				modelFound = true
				// Check if model is supported by detected devices
				modelSupported = isModelSupported(model, modelsResp.DetectedDevices)
				break
			}
		}
		
		// If model found and not supported, show warning and ask for confirmation
		if modelFound && !modelSupported {
			fmt.Println()
			fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
			fmt.Println("│                        ⚠️  WARNING                              │")
			fmt.Println("└─────────────────────────────────────────────────────────────────┘")
			fmt.Println()
			fmt.Printf("  Model '%s' is NOT supported by your current AI accelerator.\n", opts.Model)
			fmt.Println()
			fmt.Println("  This model may:")
			fmt.Println("    • Fail to start or run")
			fmt.Println("    • Experience compatibility issues")
			fmt.Println("    • Require unsupported features or operations")
			fmt.Println()
			fmt.Println("  It is recommended to use models compatible with your hardware.")
			fmt.Println()
			fmt.Print("  Do you want to proceed anyway? (y/N): ")
			
			// Read user input
			reader := bufio.NewReader(os.Stdin)
			response, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read user input: %w", err)
			}
			
			// Trim whitespace and convert to lowercase
			response = strings.TrimSpace(strings.ToLower(response))
			
			// Only proceed if user explicitly types 'y' or 'yes'
			if response != "y" && response != "yes" {
				fmt.Println()
				fmt.Println("Pull operation cancelled.")
				return nil
			}
			
			fmt.Println()
		}
	}

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
