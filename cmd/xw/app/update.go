package app

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tsingmaoai/xw-cli/cmd/xw/client"
)

// UpdateOptions holds options for the update command.
type UpdateOptions struct {
	*GlobalOptions

	// Version specifies a specific version to update to
	Version string

	// List shows available versions
	List bool

	// ShowCurrent shows the current configuration version
	ShowCurrent bool
}

// NewUpdateCommand creates the update command.
//
// The update command manages configuration version updates. It communicates
// with the xw server to download and switch configuration versions.
//
// Usage:
//
//	xw update                    # Update to latest compatible version
//	xw update --version v0.0.2   # Update to specific version
//	xw update --list             # List available versions
//	xw update --show-current     # Show current version
//
// Examples:
//
//	# Update to latest compatible configuration version
//	xw update
//
//	# Update to a specific version
//	xw update --version v0.0.3
//
//	# List all available versions
//	xw update --list
//
//	# Show current configuration version
//	xw update --show-current
func NewUpdateCommand(opts *GlobalOptions) *cobra.Command {
	updateOpts := &UpdateOptions{
		GlobalOptions: opts,
	}

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update configuration to a new version",
		Long: `Update configuration to a new version.

By default, updates to the latest version compatible with your xw binary.
You can also specify a specific version or list available versions.

The configuration version determines which devices.yaml, models.yaml, and
runtime_params.yaml files are used. Different versions may support different
hardware accelerators or have different default settings.

After updating, you must restart the xw server for changes to take effect.`,
		Example: `  # Update to latest compatible version
  xw update

  # Update to specific version
  xw update --version v0.0.2

  # List available versions
  xw update --list

  # Show current version
  xw update --show-current`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(updateOpts)
		},
	}

	cmd.Flags().StringVar(&updateOpts.Version, "version", "",
		"specific version to update to (e.g., v0.0.2)")
	cmd.Flags().BoolVar(&updateOpts.List, "list", false,
		"list available versions")
	cmd.Flags().BoolVar(&updateOpts.ShowCurrent, "show-current", false,
		"show current configuration version")

	return cmd
}

// runUpdate executes the update command logic.
func runUpdate(opts *UpdateOptions) error {
	// Create API client
	c := getClient(opts.GlobalOptions)

	// Handle show current
	if opts.ShowCurrent {
		return showCurrentVersion(c)
	}

	// Handle list
	if opts.List {
		return listVersions(c)
	}

	// Handle update
	return performUpdate(c, opts.Version)
}

// showCurrentVersion displays the current configuration version.
func showCurrentVersion(c *client.Client) error {
	resp, err := c.GetCurrentVersion()
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	fmt.Printf("Current configuration version: %s\n", resp.ConfigVersion)

	if resp.Installed {
		fmt.Println("✓ Configuration files present")
	} else {
		fmt.Println("⚠ Configuration files not found (run 'xw update' to download)")
	}

	return nil
}

// listVersions lists all available versions from the registry.
func listVersions(c *client.Client) error {
	resp, err := c.ListVersions()
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	fmt.Println("Available Configuration Versions:")
	fmt.Printf("Current xw version: %s\n", resp.CurrentXwVersion)
	fmt.Printf("Current config: %s", resp.CurrentConfigVersion)
	
	// Check if current version is installed
	for _, installed := range resp.InstalledVersions {
		if installed == resp.CurrentConfigVersion {
			fmt.Print(" ✓")
			break
		}
	}
	fmt.Println("\n")

	// Show compatible versions
	if len(resp.CompatibleVersions) > 0 {
		fmt.Println("Compatible versions (can be used with current xw):")
		for _, pkg := range resp.CompatibleVersions {
			status := ""
			if pkg.Version == resp.CurrentConfigVersion {
				status = " ✓ (current)"
			} else {
				// Check if installed
				for _, installed := range resp.InstalledVersions {
					if installed == pkg.Version {
						status = " (installed)"
						break
					}
				}
			}

			fmt.Printf("  %-12s (%s) - %s%s\n",
				pkg.Version, pkg.ReleaseDate, pkg.Description, status)
		}
		fmt.Println()
	}

	// Show incompatible versions
	if len(resp.IncompatibleVersions) > 0 {
		fmt.Println("Requires newer xw binary:")
		for _, pkg := range resp.IncompatibleVersions {
			fmt.Printf("  %-12s (requires xw >= %s)\n",
				pkg.Version, pkg.MinXwVersion)
		}
	}

	return nil
}

// performUpdate updates to a specific version or latest.
func performUpdate(c *client.Client, targetVersion string) error {
	if targetVersion == "" {
		fmt.Println("Checking for updates...")
	} else {
		fmt.Printf("Updating to version %s...\n", targetVersion)
	}

	resp, err := c.Update(targetVersion)
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("update failed: %s", resp.Message)
	}

	// Display results
	fmt.Printf("Previous version: %s\n", resp.PreviousVersion)
	fmt.Printf("New version:      %s\n", resp.NewVersion)

	if resp.Downloaded {
		fmt.Printf("\n✓ Downloaded: %s.tar.gz\n", resp.NewVersion)
		fmt.Println("✓ SHA256 verified")
		fmt.Printf("✓ Extracted to ~/.xw/%s/\n", resp.NewVersion)
	}

	fmt.Printf("\n✓ %s\n", resp.Message)

	if resp.RestartRequired {
		fmt.Println("\n⚠ Please restart the server to apply changes:")
		fmt.Println("  xw restart")
		fmt.Println("  or")
		fmt.Println("  sudo systemctl restart xw-server")
	}

	return nil
}
