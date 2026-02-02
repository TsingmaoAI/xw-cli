package app

import (
	"fmt"

	"github.com/spf13/cobra"
)

// StopOptions holds options for the stop command
type StopOptions struct {
	*GlobalOptions

	// Alias is the instance alias to stop
	Alias string

	// Force forces stop even if instance is in use
	Force bool
}

// NewStopCommand creates the stop command.
//
// The stop command stops and removes a running model instance.
//
// Usage:
//
//	xw stop ALIAS [OPTIONS]
//
// Examples:
//
//	# Stop and remove an instance
//	xw stop my-model
//
//	# Force stop and remove
//	xw stop test --force
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for stopping instances
func NewStopCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &StopOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "stop ALIAS",
		Short: "Stop and remove a running model instance",
		Long: `Stop and remove a running model instance by its alias.

The alias can be found using 'xw ps'. Stopping an instance will:
  - Stop the backend process/container
  - Remove the container and free resources
  - Permanently delete the instance

Use --force to stop an instance even if it's currently processing requests.`,
		Example: `  # Stop and remove an instance
  xw stop my-model

  # Force stop and remove
  xw stop test --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Alias = args[0]
			return runStop(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false,
		"force stop even if instance is in use")

	return cmd
}

// runStop executes the stop command logic
// Now it stops AND removes the instance (equivalent to rmi)
func runStop(opts *StopOptions) error {
	client := getClient(opts.GlobalOptions)

	// Stop and remove the instance via server API (using alias)
	// This now calls the remove API with force flag
	err := client.RemoveInstanceByAlias(opts.Alias, true)
	if err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	fmt.Printf("Stopped and removed instance: %s\n", opts.Alias)

	return nil
}

