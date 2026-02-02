package app

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsingmaoai/xw-cli/cmd/xw/client"
	"github.com/tsingmaoai/xw-cli/internal/api"
)

// ListOptions holds options for the list command
type ListOptions struct {
	*GlobalOptions
	All bool // Show all models supported by current device
}

// NewListCommand creates the list (ls) command.
//
// The list command displays available AI models, with optional filtering
// by device type. It corresponds to 'kubectl get' in Kubernetes.
//
// Usage:
//
//	xw ls [-a|--all] [-d|--device DEVICE]
//
// Examples:
//
//	# List all models for available devices
//	xw ls
//
//	# List all models in the registry
//	xw ls -a
//
//	# List models compatible with Ascend devices
//	xw ls -d ascend
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for listing models
func NewListCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &ListOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List downloaded models",
		Long: `List models that have been downloaded.

By default, only shows models that are currently downloaded and available locally.
Use -a/--all to show all models supported by the current chip.`,
		Example: `  # List downloaded models
  xw ls
  
  # List all models supported by current chip
  xw ls -a
  
  # Same as ls
  xw list`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "show all models supported by current chip")

	return cmd
}

// runList executes the list command logic.
//
// This function queries the server for available models and displays them
// in a formatted table.
//
// Parameters:
//   - opts: List command options
//
// Returns:
//   - nil on success
//   - error if the request fails or no server is available
func runList(opts *ListOptions) error {
	client := getClient(opts.GlobalOptions)

	if opts.All {
		// List all models supported by current chip
		return listAllModels(client)
	}

	// Query downloaded models from server
	models, err := client.ListDownloadedModels()
	if err != nil {
		return fmt.Errorf("failed to list models: %w", err)
	}

	if len(models) == 0 {
		fmt.Println("No models downloaded.")
		fmt.Println()
		fmt.Println("Download a model with: xw pull <model>")
		fmt.Println("List all supported models with: xw ls -a")
		return nil
	}

	// Sort models by ID for consistent output
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	// Display models in a formatted table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "MODEL\tSOURCE\tTAG\tSIZE\tDEFAULT ENGINE\tMODIFIED")

	for _, model := range models {
		// Use default values if fields are empty
		tag := model.Tag
		if tag == "" {
			tag = "latest"
		}
		
		sizeStr := formatSize(model.Size)
		
		engine := model.DefaultEngine
		if engine == "" {
			engine = "vllm:docker"
		}
		
		modifiedStr := "-"
		if model.ModifiedAt != "" {
			if t, err := time.Parse(time.RFC3339, model.ModifiedAt); err == nil {
				modifiedStr = formatTimeAgo(t)
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			model.ID,
			model.Source,
			tag,
			sizeStr,
			engine,
			modifiedStr)
	}

	w.Flush()
	fmt.Println()

	return nil
}

// listAllModels lists all models supported by the current chip.
func listAllModels(c *client.Client) error {
	// Get all models from registry with showAll=true to include unsupported models
	resp, err := c.ListModelsWithStats(api.DeviceTypeAll, true)
	if err != nil {
		return fmt.Errorf("failed to list models: %w", err)
	}

	if len(resp.Models) == 0 {
		fmt.Println("No models available.")
		return nil
	}

	// Separate models into supported and unsupported
	var supportedModels []api.Model
	var unsupportedModels []api.Model
	
	for _, model := range resp.Models {
		if isModelSupported(model, resp.DetectedDevices) {
			supportedModels = append(supportedModels, model)
		} else {
			unsupportedModels = append(unsupportedModels, model)
		}
	}

	// Sort both lists by name
	sort.Slice(supportedModels, func(i, j int) bool {
		return supportedModels[i].Name < supportedModels[j].Name
	})
	sort.Slice(unsupportedModels, func(i, j int) bool {
		return unsupportedModels[i].Name < unsupportedModels[j].Name
	})

	// Display models in a formatted table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "MODEL\tSOURCE\tTAG\tSIZE\tDEFAULT ENGINE\tMODIFIED")

	// Display supported models first
	for _, model := range supportedModels {
		printModelRow(w, model)
	}

	w.Flush()
	
	// Add separator with statistics if there are unsupported models
	if len(unsupportedModels) > 0 {
		fmt.Println()
		fmt.Printf("──────────────────────────────────── Available: %d, Not supported: %d ────────────────────────────────────\n", 
			len(supportedModels), len(unsupportedModels))
		fmt.Println()
		fmt.Fprintln(w, "MODEL\tSOURCE\tTAG\tSIZE\tDEFAULT ENGINE\tMODIFIED")
		
		// Display unsupported models
		for _, model := range unsupportedModels {
			printModelRow(w, model)
		}
		w.Flush()
	}
	
	fmt.Println()

	return nil
}

// isModelSupported checks if a model is supported by the detected devices.
func isModelSupported(model api.Model, detectedDevices []api.DeviceType) bool {
	// If no devices detected, model is not supported
	if len(detectedDevices) == 0 {
		return false
	}
	
	// Check if any of the model's supported devices matches detected devices
	for _, modelDevice := range model.SupportedDevices {
		for _, detectedDevice := range detectedDevices {
			if modelDevice == detectedDevice {
				return true
			}
		}
	}
	
	return false
}

// printModelRow prints a single model row in the table.
func printModelRow(w *tabwriter.Writer, model api.Model) {
	// Use values from API, with fallbacks for missing data
	source := model.Source
	if source == "" {
		source = "-"
	}
	
	tag := model.Tag
	if tag == "" {
		tag = "-"
	}
	
	// Size: only show for downloaded models
	sizeStr := "-"
	if model.Status == "downloaded" && model.ModifiedAt != "" {
		sizeStr = formatSize(model.Size)
	}
	
	engine := model.DefaultEngine
	if engine == "" {
		engine = "-"
	}
	
	modifiedStr := "-"
	if model.ModifiedAt != "" {
		if t, err := time.Parse(time.RFC3339, model.ModifiedAt); err == nil {
			modifiedStr = formatTimeAgo(t)
		}
	}

	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
		model.Name,
		source,
		tag,
		sizeStr,
		engine,
		modifiedStr)
}

// formatSize converts bytes to a human-readable size string.
//
// Parameters:
//   - bytes: Size in bytes
//
// Returns:
//   - Formatted size string (e.g., "8.0GB", "1.5TB")
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f%cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// formatStatus formats the download status of a model.
//
// Parameters:
//   - status: Model status ("not_downloaded", "downloading", "downloaded")
//
// Returns:
//   - Formatted status string with visual indicator
func formatStatus(status string) string {
	switch status {
	case "downloaded":
		return "✓"
	case "downloading":
		return "..."
	case "not_downloaded":
		return "-"
	default:
		return "-"
	}
}

// truncate shortens a string to maxLen characters, adding "..." if truncated.
//
// Parameters:
//   - s: The string to truncate
//   - maxLen: Maximum length
//
// Returns:
//   - Truncated string
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen-3] + "..."
}

// formatTimeAgo converts a time to a human-readable "time ago" string.
//
// Parameters:
//   - t: The time to format
//
// Returns:
//   - Human-readable time ago string
func formatTimeAgo(t time.Time) string {
	duration := time.Since(t)
	
	if duration < time.Minute {
		return "just now"
	} else if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else if duration < 7*24*time.Hour {
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	} else if duration < 30*24*time.Hour {
		weeks := int(duration.Hours() / (24 * 7))
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	} else if duration < 365*24*time.Hour {
		months := int(duration.Hours() / (24 * 30))
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	} else {
		years := int(duration.Hours() / (24 * 365))
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}
