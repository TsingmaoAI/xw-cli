package app

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tsingmao/xw/internal/api"
)

// ListOptions holds options for the list command
type ListOptions struct {
	*GlobalOptions

	// All shows all available models
	All bool

	// Device filters models by device type
	Device string
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
		Use:   "ls",
		Short: "List available models",
		Long: `List available AI models in the xw registry.

By default, lists models compatible with detected devices. Use --all to
show all models regardless of device compatibility, or --device to filter
by a specific device type.

Supported device types:
  - kunlun   : Baidu Kunlun XPU
  - ascend   : Huawei Ascend NPU
  - hygon    : Hygon processor
  - loongson : Loongson (Longxin) processor`,
		Example: `  # List models for detected devices
  xw ls

  # List all available models
  xw ls -a

  # List models for Kunlun devices
  xw ls -d kunlun

  # List models for Ascend devices
  xw ls --device ascend`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false,
		"show all models")
	cmd.Flags().StringVarP(&opts.Device, "device", "d", "",
		"filter by device type (kunlun, ascend, hygon, loongson)")

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

	// Determine device type filter
	deviceType := api.DeviceTypeAll
	if opts.Device != "" {
		deviceType = api.DeviceType(opts.Device)
	}

	// Query models from server (returns full response with statistics)
	resp, err := client.ListModelsWithStats(deviceType, opts.All)
	if err != nil {
		return fmt.Errorf("failed to list models: %w", err)
	}

	if len(resp.Models) == 0 {
		fmt.Println("No models found.")
		return nil
	}

	// Sort models by name for consistent output
	sort.Slice(resp.Models, func(i, j int) bool {
		return resp.Models[i].Name < resp.Models[j].Name
	})

	// Display models in a formatted table with download status
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tSIZE\tSTATUS\tDESCRIPTION")

	for _, model := range resp.Models {
		size := formatSize(model.Size)
		status := formatStatus(model.Status)

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			model.Name,
			model.Version,
			size,
			status,
			truncate(model.Description, 50))
	}

	w.Flush()
	fmt.Println()

	// Display statistics
	if opts.All {
		// When showing all models
		fmt.Printf("Total: %d models in registry.\n", resp.TotalModels)
	} else {
		// When showing available models only
		if len(resp.DetectedDevices) == 0 {
			fmt.Printf("No AI accelerators detected. Showing %d of %d models. Use -a to list all.\n",
				len(resp.Models), resp.TotalModels)
		} else {
			devicesStr := formatDeviceList(resp.DetectedDevices)
			fmt.Printf("Detected: %s | Showing %d of %d models compatible with your hardware. Use -a to list all.\n",
				devicesStr, resp.AvailableModels, resp.TotalModels)
		}
	}

	return nil
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

// formatDeviceList converts a device type slice to a human-readable string.
//
// Parameters:
//   - devices: Slice of device types
//
// Returns:
//   - Human-readable device list string
func formatDeviceList(devices []api.DeviceType) string {
	if len(devices) == 0 {
		return "none"
	}

	result := string(devices[0])
	for i := 1; i < len(devices); i++ {
		result += ", " + string(devices[i])
	}

	return result
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
