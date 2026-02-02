package app

import (
	"fmt"

	"github.com/spf13/cobra"
)

// VersionOptions holds options for the version command
type VersionOptions struct {
	*GlobalOptions

	// Client shows only client version
	Client bool

	// Server shows only server version
	Server bool
}

// NewVersionCommand creates the version command.
//
// The version command displays version information for the CLI client and/or
// the xw server. It corresponds to 'kubectl version' in Kubernetes.
//
// Usage:
//
//	xw version [--client] [--server]
//
// Examples:
//
//	# Show both client and server versions
//	xw version
//
//	# Show only client version
//	xw version --client
//
//	# Show only server version
//	xw version --server
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for displaying version info
func NewVersionCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &VersionOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Display version information",
		Long: `Display version information for the xw client and server.

By default, shows version information for both the client and server. Use
--client or --server to show only one.`,
		Example: `  # Show both client and server versions
  xw version

  # Show only client version
  xw version --client

  # Show only server version
  xw version --server`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVersion(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Client, "client", false,
		"show client version only")
	cmd.Flags().BoolVar(&opts.Server, "server", false,
		"show server version only")

	return cmd
}

// runVersion executes the version command logic.
//
// This function displays version information for the client and/or server
// based on the command options.
//
// Parameters:
//   - opts: Version command options
//
// Returns:
//   - nil on success
//   - error if server query fails (when requesting server version)
func runVersion(opts *VersionOptions) error {
	showClient := opts.Client || (!opts.Client && !opts.Server)
	showServer := opts.Server || (!opts.Client && !opts.Server)

	// Show client version
	if showClient {
		fmt.Println("Client Version:")
		fmt.Println("  Version:    1.0.0")
		fmt.Println("  Build Time: 2026-01-26")
		fmt.Println("  Git Commit: dev")
	}

	// Show server version
	if showServer {
		if showClient {
			fmt.Println()
		}

		client := getClient(opts.GlobalOptions)
		resp, err := client.Version()
		if err != nil {
			return fmt.Errorf("failed to get server version: %w", err)
		}

		fmt.Println("Server Version:")
		fmt.Printf("  Version:    %s\n", resp.Version)
		fmt.Printf("  Build Time: %s\n", resp.BuildTime)
		fmt.Printf("  Git Commit: %s\n", resp.GitCommit)
	}

	return nil
}
