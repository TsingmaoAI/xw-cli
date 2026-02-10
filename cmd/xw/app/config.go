package app

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ConfigOptions holds options for the config command
type ConfigOptions struct {
	*GlobalOptions
}

// NewConfigCommand creates the config command and its subcommands.
//
// The config command group manages server configuration settings.
// It provides subcommands for viewing and modifying configuration values
// such as server name and registry URL.
//
// Available subcommands:
//   - info: Display all configuration settings
//   - get:  Get a specific configuration value
//   - set:  Set a configuration value
//
// Usage:
//
//	xw config <subcommand> [flags]
//
// Examples:
//
//	# View all configuration
//	xw config info
//
//	# Get server name
//	xw config get name
//
//	# Set registry URL
//	xw config set registry https://custom.registry.com/packages.json
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for managing configuration
func NewConfigCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &ConfigOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage server configuration",
		Long: `Manage xw server configuration settings.

The config command group allows you to view and modify server configuration
values such as the server name and registry URL. All configuration changes
are immediately persisted and take effect without requiring a server restart.`,
		Example: `  # View all configuration
  xw config info

  # Get specific configuration value
  xw config get name

  # Set configuration value
  xw config set registry https://custom.registry.com/packages.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Show help if no subcommand specified
			return cmd.Help()
		},
	}

	// Add subcommands
	cmd.AddCommand(NewConfigInfoCommand(opts))
	cmd.AddCommand(NewConfigGetCommand(opts))
	cmd.AddCommand(NewConfigSetCommand(opts))

	return cmd
}

// NewConfigInfoCommand creates the config info subcommand.
//
// This command displays all current server configuration settings including
// server identity, network configuration, and storage paths.
//
// Usage:
//
//	xw config info
//
// Returns:
//   - A configured cobra.Command for displaying configuration
func NewConfigInfoCommand(opts *ConfigOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Display all configuration settings",
		Long: `Display all current server configuration settings.

This command shows comprehensive information about the server configuration
including:
  - Server name (unique identifier)
  - Registry URL (configuration package source)
  - Host address
  - Port number
  - Configuration directory path
  - Data directory path`,
		Example: `  # Display all configuration
  xw config info`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigInfo(opts)
		},
	}

	return cmd
}

// NewConfigGetCommand creates the config get subcommand.
//
// This command retrieves the value of a specific configuration key.
//
// Supported keys:
//   - name: Server instance identifier
//   - registry: Configuration package registry URL
//   - host: Server host address
//   - port: Server port number
//   - config_dir: Configuration directory path
//   - data_dir: Data directory path
//
// Usage:
//
//	xw config get <key>
//
// Returns:
//   - A configured cobra.Command for getting configuration values
func NewConfigGetCommand(opts *ConfigOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Long: `Get the value of a specific configuration key.

Supported configuration keys:
  - name:       Server instance identifier
  - registry:   Configuration package registry URL
  - host:       Server host address
  - port:       Server port number
  - config_dir: Configuration directory path
  - data_dir:   Data directory path`,
		Example: `  # Get server name
  xw config get name

  # Get registry URL
  xw config get registry

  # Get server port
  xw config get port`,
		Args: cobra.ExactArgs(1),
		ValidArgs: []string{"name", "registry", "host", "port", "config_dir", "data_dir"},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			return runConfigGet(opts, key)
		},
	}

	return cmd
}

// NewConfigSetCommand creates the config set subcommand.
//
// This command sets the value of a specific configuration key.
// Changes are immediately persisted to disk.
//
// Supported keys:
//   - registry: Configuration package registry URL
//
// Note: Server name, host, and port cannot be modified via this command.
// Edit server.conf manually or use command-line flags for host/port.
//
// Usage:
//
//	xw config set <key> <value>
//
// Returns:
//   - A configured cobra.Command for setting configuration values
func NewConfigSetCommand(opts *ConfigOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long: `Set the value of a specific configuration key.

Supported configuration keys:
  - registry: Configuration package registry URL (must be valid HTTP/HTTPS URL)

Note: Server name, host, and port cannot be modified via this command.
  - name: Tied to running container instances (modification would break instance management)
  - host/port: Use command-line flags (--host, --port) or edit server.conf manually

Changes are immediately persisted to disk and take effect without server restart.`,
		Example: `  # Set registry URL
  xw config set registry https://custom.registry.com/packages.json`,
		Args: cobra.ExactArgs(2),
		ValidArgs: []string{"registry"},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value := args[1]
			return runConfigSet(opts, key, value)
		},
	}

	return cmd
}

// runConfigInfo executes the config info command logic.
//
// This function calls the server API to retrieve all configuration settings
// and displays them in a formatted table.
//
// Parameters:
//   - opts: Config command options
//
// Returns:
//   - nil on success
//   - error if API call fails or server is unreachable
func runConfigInfo(opts *ConfigOptions) error {
	c := getClient(opts.GlobalOptions)

	config, err := c.GetConfigInfo()
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	// Display configuration in formatted output
	fmt.Println("Server Configuration:")
	fmt.Println("=====================")
	fmt.Printf("Name:       %s\n", config.Name)
	fmt.Printf("Registry:   %s\n", config.Registry)
	fmt.Printf("Host:       %s\n", config.Host)
	fmt.Printf("Port:       %d\n", config.Port)
	fmt.Printf("Config Dir: %s\n", config.ConfigDir)
	fmt.Printf("Data Dir:   %s\n", config.DataDir)

	return nil
}

// runConfigGet executes the config get command logic.
//
// This function calls the server API to retrieve a specific configuration
// value and displays it.
//
// Parameters:
//   - opts: Config command options
//   - key: Configuration key to retrieve
//
// Returns:
//   - nil on success
//   - error if API call fails or key is not found
func runConfigGet(opts *ConfigOptions, key string) error {
	c := getClient(opts.GlobalOptions)

	value, err := c.GetConfigValue(key)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	fmt.Println(value)

	return nil
}

// runConfigSet executes the config set command logic.
//
// This function calls the server API to update a specific configuration
// value. The change is immediately persisted to disk.
//
// Parameters:
//   - opts: Config command options
//   - key: Configuration key to set
//   - value: New value for the configuration key
//
// Returns:
//   - nil on success
//   - error if API call fails or validation fails
func runConfigSet(opts *ConfigOptions, key, value string) error {
	c := getClient(opts.GlobalOptions)

	if err := c.SetConfigValue(key, value); err != nil {
		return fmt.Errorf("failed to set configuration: %w", err)
	}

	fmt.Printf("✓ Configuration updated: %s = %s\n", key, value)

	return nil
}

