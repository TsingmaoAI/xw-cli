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

	// Pull model with progress callback
	var lastWasProgress bool
	fileLines := make(map[string]int) // Track which line each file is on
	lineOrder := []string{}           // Track order of files
	currentLine := 0                  // Current cursor line (0-based from first progress)
	
	resp, err := client.Pull(opts.Model, "", func(message string) {
		// Display progress messages with smart formatting
		isProgress := strings.Contains(message, "%")
		
		if isProgress {
			// Extract filename from progress message (format: "Downloading filename: x%")
			var currentFile string
			if idx := strings.Index(message, ":"); idx != -1 {
				currentFile = strings.TrimSpace(message[:idx])
				currentFile = strings.TrimPrefix(currentFile, "Downloading ")
			}
			
			if currentFile == "" {
				// Fallback if we can't extract filename
			fmt.Printf("\r%s", message)
				lastWasProgress = true
				return
			}
			
			lineNum, exists := fileLines[currentFile]
			if !exists {
				// New file - assign it the next line
				lineNum = len(lineOrder)
				fileLines[currentFile] = lineNum
				lineOrder = append(lineOrder, currentFile)
				
				// If this is not the first file, move to new line
				if lastWasProgress {
					fmt.Println()
					currentLine++
				}
				fmt.Printf("%s", message)
			} else {
				// Existing file - move to its line and update
				if lineNum != currentLine {
					// Move cursor up/down to the file's line
					lineDiff := currentLine - lineNum
					if lineDiff > 0 {
						// Move up
						fmt.Printf("\033[%dA", lineDiff)
					} else if lineDiff < 0 {
						// Move down
						fmt.Printf("\033[%dB", -lineDiff)
					}
					currentLine = lineNum
				}
				// Overwrite the line
				fmt.Printf("\r%s\033[K", message) // \033[K clears to end of line
				
				// Move cursor back to the bottom
				if len(lineOrder) > 0 && currentLine < len(lineOrder)-1 {
					linesToBottom := len(lineOrder) - 1 - currentLine
					fmt.Printf("\033[%dB", linesToBottom)
					currentLine = len(lineOrder) - 1
				}
			}
			lastWasProgress = true
		} else {
			// Status message - print on new line
			if lastWasProgress {
				fmt.Println() // End previous progress line
			}
			fmt.Println(message)
			lastWasProgress = false
			// Reset tracking for next batch of files
			fileLines = make(map[string]int)
			lineOrder = []string{}
			currentLine = 0
		}
	})
	
	// Add empty line after stream ends to separate from result
	if lastWasProgress {
		fmt.Println() // Ensure we end with a newline after progress
	}
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
