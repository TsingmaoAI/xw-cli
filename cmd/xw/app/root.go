// Package app provides the command-line interface implementation for xw.
//
// This package contains all CLI commands and their implementations, following
// the Kubernetes CLI architecture pattern with cobra. Commands are organized
// hierarchically with a root command and subcommands.
package app

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsingmao/xw/cmd/xw/client"
)

const (
	// cliName is the name of the CLI application
	cliName = "xw"

	// cliDescription is the short description shown in help text
	cliDescription = "xw - AI inference on domestic chips"

	// envServerURL is the environment variable name for server URL
	envServerURL = "XW_SERVER"

	// defaultServerURL is the default server address
	defaultServerURL = "http://localhost:11581"
)

// GlobalOptions holds options that are common to all commands
type GlobalOptions struct {
	// ServerURL is the xw server address
	ServerURL string

	// Verbose enables verbose output
	Verbose bool
}

// NewXWCommand creates the root xw command with all subcommands.
//
// The root command provides the main entry point for the CLI. It sets up
// global flags, initializes configuration, and registers all subcommands.
//
// Returns:
//   - A configured cobra.Command ready for execution
//
// Example:
//
//	cmd := NewXWCommand()
//	if err := cmd.Execute(); err != nil {
//	    os.Exit(1)
//	}
func NewXWCommand() *cobra.Command {
	opts := &GlobalOptions{}

	cmd := &cobra.Command{
		Use:   cliName,
		Short: cliDescription,
		Long: `xw is a command-line tool for AI model inference on domestic chip architectures.

It provides a simple interface to list, download, and execute AI models
optimized for Chinese-made chips including Ascend NPU.

The xw CLI communicates with a local server process over HTTP. Make sure the
xw server is running before executing commands.`,
		SilenceUsage: true,
		// SilenceErrors is false by default - we want to show errors to users
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true, // Disable auto-generated completion command
		},
	}

	// Add global flags
	cmd.PersistentFlags().StringVar(&opts.ServerURL, "server", "",
		fmt.Sprintf("xw server address (env: %s, default: %s)", envServerURL, defaultServerURL))
	cmd.PersistentFlags().BoolVarP(&opts.Verbose, "verbose", "v", false,
		"verbose output")

	// Add subcommands
	cmd.AddCommand(
		NewListCommand(opts),
		NewShowCommand(opts),
		NewRunCommand(opts),
		NewStartCommand(opts),
		NewPsCommand(opts),
		NewStopCommand(opts),
		NewRmiCommand(opts),
		NewLogsCommand(opts),
		NewPullCommand(opts),
		NewVersionCommand(opts),
		NewServeCommand(opts),
		NewDeviceCommand(opts),
	)

	return cmd
}

// getClient creates and returns a configured API client.
//
// This helper function initializes an HTTP client for communicating with
// the xw server. It determines the server address using the following priority:
//   1. --server flag (if specified)
//   2. XW_SERVER environment variable (if set)
//   3. Default: http://localhost:11581
//
// Parameters:
//   - opts: Global options containing server URL
//
// Returns:
//   - A configured client.Client instance
func getClient(opts *GlobalOptions) *client.Client {
	serverURL := opts.ServerURL
	
	// Priority: flag > environment variable > default
	if serverURL == "" {
		serverURL = os.Getenv(envServerURL)
	}
	if serverURL == "" {
		serverURL = defaultServerURL
	}
	
	return client.NewClient(serverURL)
}

// checkError prints an error and exits if err is not nil.
//
// This is a convenience function for fatal error handling in CLI commands.
// It prints the error to stderr and exits with code 1.
//
// Parameters:
//   - err: The error to check
func checkError(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
