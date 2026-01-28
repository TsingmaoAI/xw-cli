package app

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// PsOptions holds options for the ps command
type PsOptions struct {
	*GlobalOptions

	// All shows all instances (including stopped)
	All bool
}

// NewPsCommand creates the ps command.
//
// The ps command lists running model instances, similar to 'docker ps'.
//
// Usage:
//
//	xw ps [OPTIONS]
//
// Examples:
//
//	# List running instances
//	xw ps
//
//	# List all instances (including stopped)
//	xw ps --all
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for listing instances
func NewPsCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &PsOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:     "ps",
		Short:   "List running model instances",
		Aliases: []string{"list"},
		Long: `List running model instances with their status and configuration.

By default, only running instances are shown. Use --all to see all instances
including stopped ones.`,
		Example: `  # List running instances
  xw ps

  # List all instances
  xw ps --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPs(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false,
		"show all instances (including stopped)")

	return cmd
}

// runPs executes the ps command logic
func runPs(opts *PsOptions) error {
	client := getClient(opts.GlobalOptions)

	// Get instances from server
	instances, err := client.ListInstances(opts.All)
	if err != nil {
		return fmt.Errorf("failed to list instances: %w", err)
	}

	if len(instances) == 0 {
		fmt.Println("No running instances")
		fmt.Println()
		fmt.Println("Start a model with: xw run <model>")
		return nil
	}

	// Display instances in a table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "INSTANCE ID\tMODEL\tBACKEND\tMODE\tSTATE\tUPTIME")

	for _, instance := range instances {
		instanceMap, ok := instance.(map[string]interface{})
		if !ok {
			continue
		}

		id, _ := instanceMap["id"].(string)
		modelID, _ := instanceMap["model_id"].(string)
		backendType, _ := instanceMap["backend_type"].(string)
		deploymentMode, _ := instanceMap["deployment_mode"].(string)
		state, _ := instanceMap["state"].(string)

		// Calculate uptime
		var uptime string
		if startedAtStr, ok := instanceMap["started_at"].(string); ok && startedAtStr != "" {
			startedAt, err := time.Parse(time.RFC3339, startedAtStr)
			if err == nil {
				elapsed := time.Since(startedAt)
				uptime = formatDuration(elapsed)
			}
		} else {
			uptime = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			id,
			modelID,
			backendType,
			deploymentMode,
			state,
			uptime)
	}

	w.Flush()

	return nil
}

// formatDuration formats a duration in human-readable format
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	} else {
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd", days)
	}
}

