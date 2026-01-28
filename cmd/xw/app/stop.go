package app

import (
	"fmt"

	"github.com/spf13/cobra"
)

// StopOptions holds options for the stop command
type StopOptions struct {
	*GlobalOptions

	// InstanceID is the instance to stop
	InstanceID string

	// Force forces stop even if instance is in use
	Force bool
}

// NewStopCommand creates the stop command.
//
// The stop command stops a running model instance.
//
// Usage:
//
//	xw stop INSTANCE_ID [OPTIONS]
//
// Examples:
//
//	# Stop an instance
//	xw stop qwen2-0.5b-mindie-docker
//
//	# Force stop
//	xw stop qwen2-7b-vllm-native --force
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
		Use:   "stop INSTANCE_ID",
		Short: "Stop a running model instance",
		Long: `Stop a running model instance by its instance ID.

The instance ID can be found using 'xw ps'. Stopping an instance will:
  - Terminate the backend process/container
  - Free up system resources
  - Remove the instance from the running list

Use --force to stop an instance even if it's currently processing requests.`,
		Example: `  # Stop an instance
  xw stop qwen2-0.5b-mindie-docker

  # Force stop
  xw stop qwen2-7b-vllm-native --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.InstanceID = args[0]
			return runStop(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false,
		"force stop even if instance is in use")

	return cmd
}

// runStop executes the stop command logic
func runStop(opts *StopOptions) error {
	client := getClient(opts.GlobalOptions)

	// Stop the instance via server API
	err := client.StopInstance(opts.InstanceID, opts.Force)
	if err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	fmt.Printf("Stopped instance: %s\n", opts.InstanceID)

	return nil
}

