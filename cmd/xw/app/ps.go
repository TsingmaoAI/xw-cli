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
		Short:   "List model instances",
		Aliases: []string{"list"},
		Long: `List all model instances with their status and configuration.

Shows all instances including both running and stopped ones.`,
		Example: `  # List all instances
  xw ps`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPs(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", true,
		"show all instances (default: true)")

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
		fmt.Println("No instances found")
		fmt.Println()
		fmt.Println("Start a model with: xw start <model>")
		return nil
	}

	// Display instances in a table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tMODEL\tENGINE\tSTATE\tUPTIME")

	for _, instance := range instances {
		instanceMap, ok := instance.(map[string]interface{})
		if !ok {
			continue
		}

		modelID, _ := instanceMap["model_id"].(string)
		alias, _ := instanceMap["alias"].(string)
		// If alias is empty, use model_id for backward compatibility
		if alias == "" {
			alias = modelID
		}
		backendType, _ := instanceMap["backend_type"].(string)
		deploymentMode, _ := instanceMap["deployment_mode"].(string)
		state, _ := instanceMap["state"].(string)

		// Combine backend and mode into engine format
		engine := fmt.Sprintf("%s:%s", backendType, deploymentMode)

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

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			alias,
			modelID,
			engine,
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

