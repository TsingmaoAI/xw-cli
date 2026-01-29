package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tsingmao/xw/internal/config"
	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/server"
)

// ServeOptions holds options for the serve command
type ServeOptions struct {
	*GlobalOptions

	// Host is the server host address
	Host string

	// Port is the server port
	Port int
	
	// Home is the data directory for storing models and configurations
	Home string
}

// NewServeCommand creates the serve command.
//
// The serve command starts the xw HTTP server. This is primarily for
// development and testing. In production, the server should be run as
// a systemd service using the xw-server binary.
//
// Usage:
//
//	xw serve [--host HOST] [--port PORT]
//
// Examples:
//
//	# Start server on default port (11581)
//	xw serve
//
//	# Start server on specific host and port
//	xw serve --host 0.0.0.0 --port 9090
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for starting the server
func NewServeCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &ServeOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the xw server",
		Long: `Start the xw HTTP server for handling API requests.

This command is primarily intended for development and testing. In production
deployments, use the dedicated xw-server binary with systemd.

The server listens for HTTP requests and manages model execution on domestic
chip devices. Press Ctrl+C to gracefully shut down the server.`,
		Example: `  # Start server on default settings (localhost:11581)
  xw serve

  # Start server on all interfaces
  xw serve --host 0.0.0.0

  # Start server on custom port
  xw serve --port 9090

  # Start with verbose logging
  xw serve -v`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate port range
			if opts.Port < 1 || opts.Port > 65535 {
				return fmt.Errorf("invalid port number: %d (must be between 1-65535)", opts.Port)
			}
			return runServe(opts)
		},
	}
	
	cmd.Flags().StringVar(&opts.Host, "host", "localhost",
		"server host address")
	cmd.Flags().IntVar(&opts.Port, "port", 11581,
		"server port")
	cmd.Flags().StringVar(&opts.Home, "home", "",
		"data directory for models and configurations (default: ~/.xw)")
	
	// Mark unknown flags as errors
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		cmd.Println(err)
		cmd.Println()
		cmd.Println("Use \"xw serve --help\" for more information.")
		return err
	})

	return cmd
}

// runServe executes the serve command logic.
//
// This function starts the HTTP server and handles graceful shutdown on
// interrupt signals.
//
// Parameters:
//   - opts: Serve command options
//
// Returns:
//   - nil on successful shutdown
//   - error if server startup or shutdown fails
func runServe(opts *ServeOptions) error {
	// Create configuration with custom home directory if specified
	var cfg *config.Config
	if opts.Home != "" {
		cfg = config.NewConfigWithHome(opts.Home)
	} else {
		cfg = config.NewDefaultConfig()
	}
	cfg.Server.Host = opts.Host
	cfg.Server.Port = opts.Port

	// Ensure directories exist
	if err := cfg.EnsureDirectories(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Get or create server identity
	identity, err := cfg.GetOrCreateServerIdentity()
	if err != nil {
		return fmt.Errorf("failed to get server identity: %w", err)
	}
	logger.Info("Server identity: %s", identity.Name)
	
	// Initialize runtime manager with available runtimes and server identity
	runtimeMgr, err := server.InitializeRuntimeManager()
	if err != nil {
		return fmt.Errorf("failed to initialize runtime manager: %w", err)
	}
	defer runtimeMgr.Close()
	
	// Set server name in runtime manager
	runtimeMgr.SetServerName(identity.Name)
	
	// Create server with runtime manager
	srv := server.NewServer(cfg, runtimeMgr)
	
	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Enable debug logging if verbose
	if opts.Verbose {
		logger.SetDebug(true)
	}

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		logger.Info("Press Ctrl+C to stop")
		if err := srv.Start(); err != nil {
			// Check for common errors
			if isAddressInUse(err) {
				logger.Error("Port %d is already in use", opts.Port)
				logger.Error("Please stop the existing server or use a different port with --port")
				errChan <- fmt.Errorf("address already in use: %s:%d", opts.Host, opts.Port)
				return
			}
			logger.Error("Server failed to start: %v", err)
			errChan <- err
			return
		}
		errChan <- nil
	}()
	
	// Wait for shutdown signal or error
	select {
	case <-sigChan:
		logger.Info("Received interrupt signal, shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 30)
		defer cancel()
		
		if err := srv.Stop(ctx); err != nil {
			return fmt.Errorf("server shutdown failed: %w", err)
		}
		
		logger.Info("Server stopped successfully")
		return nil
		
	case err := <-errChan:
		if err != nil {
			return err
		}
		return nil
	}
}

// isAddressInUse checks if the error is due to address already in use
func isAddressInUse(err error) bool {
	return err != nil && (
		// Linux/Unix
		containsAny(err.Error(), "bind: address already in use", "listen tcp") ||
		// Windows
		containsAny(err.Error(), "bind: Only one usage"))
}

// containsAny checks if string contains any of the substrings
func containsAny(s string, substrs ...string) bool {
	for _, substr := range substrs {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
