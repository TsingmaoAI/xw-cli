package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// LogsOptions holds options for the logs command
type LogsOptions struct {
	*GlobalOptions

	// Alias is the instance alias to get logs from
	Alias string

	// Follow continues streaming logs in real-time
	Follow bool
}

// NewLogsCommand creates the logs command.
//
// The logs command displays logs from a running instance.
//
// Usage:
//
//	xw logs ALIAS [OPTIONS]
//
// Examples:
//
//	# View logs
//	xw logs my-model
//
//	# Follow logs in real-time (like tail -f)
//	xw logs my-model -f
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for viewing logs
func NewLogsCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &LogsOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "logs ALIAS",
		Short: "View logs from a model instance",
		Long: `View logs from a running model instance.

By default, shows existing logs and exits. Use -f/--follow to stream logs in real-time.

Examples:
  # Show existing logs
  xw logs my-model

  # Follow logs in real-time (press Ctrl+C to stop)
  xw logs my-model -f
  
  # Shorter version with -f
  xw logs my-model -f`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				// Show help when no arguments provided
				cmd.Help()
				os.Exit(0)
			}
			if len(args) != 1 {
				return fmt.Errorf("accepts 1 arg, received %d", len(args))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Alias = args[0]
			return runLogs(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Follow, "follow", "f", false,
		"follow log output (stream logs in real-time)")

	return cmd
}

// runLogs executes the logs command logic
func runLogs(opts *LogsOptions) error {
	client := getClient(opts.GlobalOptions)

	// Setup signal handler for Ctrl+C when following
	if opts.Follow {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		// Start log streaming in a goroutine
		logDone := make(chan error, 1)
		go func() {
			err := client.StreamInstanceLogs(opts.Alias, true, func(logLine string) {
				fmt.Print(logLine)
				// Force flush stdout for real-time output
				os.Stdout.Sync()
			})
			logDone <- err
		}()

		// Wait for signal or log stream to end
		select {
		case <-sigChan:
			// User pressed Ctrl+C
			return nil
		case err := <-logDone:
			// Log stream ended
			if err != nil {
				return fmt.Errorf("failed to stream logs: %w", err)
			}
			return nil
		}
	}

	// Non-follow mode: just get existing logs
	err := client.StreamInstanceLogs(opts.Alias, false, func(logLine string) {
		fmt.Print(logLine)
		os.Stdout.Sync()
	})
	if err != nil {
		return fmt.Errorf("failed to get logs: %w", err)
	}

	return nil
}

