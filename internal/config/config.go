// Package config provides configuration management for the xw application.
//
// This package handles all configuration-related functionality including:
//   - Server configuration (host, port, address)
//   - Storage paths (config directory, models directory)
//   - Default values and environment-specific settings
//
// The configuration is designed to be flexible and can be customized
// for different deployment scenarios (development, production, systemd service).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultServerHost is the default server host address.
	// The server listens on localhost by default for security.
	DefaultServerHost = "localhost"

	// DefaultServerPort is the default server port.
	// Port 11581 is used as it doesn't require root privileges.
	DefaultServerPort = 11581

	// DefaultConfigDir is the default configuration directory name.
	// This directory is created in the user's home directory.
	DefaultConfigDir = ".xw"

	// DefaultModelsDir is the default models directory name.
	// Model files are stored in this subdirectory within the config directory.
	DefaultModelsDir = "models"
	
	// ServerConfigFile is the server configuration file name.
	// This file stores the current server address for client auto-discovery.
	ServerConfigFile = "server.json"
)

// ServerInfo represents the server runtime information written to server.json
//
// This file is automatically created when the server starts and removed when
// it stops. Clients read this file to auto-discover the server address.
type ServerInfo struct {
	// Address is the server URL (e.g., "http://localhost:11581")
	Address string `json:"address"`
	
	// PID is the server process ID
	PID int `json:"pid"`
	
	// StartTime is when the server was started (RFC3339 format)
	StartTime string `json:"start_time"`
	
	// Version is the server version
	Version string `json:"version,omitempty"`
}

// Config represents the complete application configuration.
//
// This is the root configuration struct that contains all settings
// required for running the xw application, including server and storage
// configurations. The struct can be serialized to/from JSON for persistence.
type Config struct {
	// Server holds the HTTP server configuration including host and port.
	Server ServerConfig `json:"server"`

	// Storage holds the storage configuration including directories for
	// configuration files and model files.
	Storage StorageConfig `json:"storage"`
}

// ServerConfig represents the HTTP server configuration.
//
// This configuration controls how the xw server listens for incoming
// HTTP connections from CLI clients or other API consumers.
type ServerConfig struct {
	// Host is the server host address (e.g., "localhost", "0.0.0.0").
	// Using "localhost" restricts access to local clients only.
	// Using "0.0.0.0" allows access from any network interface.
	Host string `json:"host"`

	// Port is the TCP port number the server listens on.
	// Common values are 11581 (default) or other non-privileged ports.
	Port int `json:"port"`

	// Address is the computed full server address.
	// This field is not serialized and is computed from Host and Port.
	// Format: "http://host:port"
	Address string `json:"-"`
}

// StorageConfig represents the storage and persistence configuration.
//
// This configuration defines where the application stores its data,
// including configuration files, model files, and other persistent state.
type StorageConfig struct {
	// ConfigDir is the absolute path to the main configuration directory.
	// This directory contains application settings and metadata.
	// Example: "/home/user/.xw"
	ConfigDir string `json:"config_dir"`

	// ModelsDir is the absolute path to the models storage directory.
	// This directory contains downloaded AI model files.
	// Example: "/home/user/.xw/models"
	ModelsDir string `json:"models_dir"`
}

// NewDefaultConfig creates a new configuration instance with default values.
//
// This function initializes a Config struct with sensible defaults suitable
// for most deployment scenarios. The configuration uses:
//   - Server: localhost:11581 for local-only access
//   - Storage: ~/.xw for configuration and models
//
// If the user's home directory cannot be determined, /tmp is used as a fallback.
//
// Returns:
//   - A pointer to a newly created Config with default values.
//
// Example:
//
//	cfg := config.NewDefaultConfig()
//	fmt.Printf("Server: %s\n", cfg.GetServerAddress())
func NewDefaultConfig() *Config {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	configDir := filepath.Join(homeDir, DefaultConfigDir)
	modelsDir := filepath.Join(configDir, DefaultModelsDir)

	return &Config{
		Server: ServerConfig{
			Host:    DefaultServerHost,
			Port:    DefaultServerPort,
			Address: fmt.Sprintf("http://%s:%d", DefaultServerHost, DefaultServerPort),
		},
		Storage: StorageConfig{
			ConfigDir: configDir,
			ModelsDir: modelsDir,
		},
	}
}

// NewConfigWithHome creates a new configuration with a custom home directory.
//
// This function allows specifying a custom data directory instead of using
// the default ~/.xw location. Useful for:
//   - Testing with isolated environments
//   - Running multiple instances
//   - Custom deployment scenarios
//
// Parameters:
//   - home: Custom home directory path
//
// Returns:
//   - A pointer to a newly created Config with the specified home directory
//
// Example:
//
//	cfg := config.NewConfigWithHome("/data/xw")
//	// Models will be stored in /data/xw/models/
func NewConfigWithHome(home string) *Config {
	modelsDir := filepath.Join(home, DefaultModelsDir)

	return &Config{
		Server: ServerConfig{
			Host:    DefaultServerHost,
			Port:    DefaultServerPort,
			Address: fmt.Sprintf("http://%s:%d", DefaultServerHost, DefaultServerPort),
		},
		Storage: StorageConfig{
			ConfigDir: home,
			ModelsDir: modelsDir,
		},
	}
}

// GetServerAddress returns the complete HTTP server address.
//
// This method constructs the full server URL from the host and port
// configuration. The returned address can be used by HTTP clients to
// connect to the server.
//
// Returns:
//   - A string in the format "http://host:port"
//
// Example:
//
//	addr := cfg.GetServerAddress()
//	// Returns: "http://localhost:11581"
func (c *Config) GetServerAddress() string {
	return fmt.Sprintf("http://%s:%d", c.Server.Host, c.Server.Port)
}

// EnsureDirectories creates all required directories if they don't exist.
//
// This method ensures that the directory structure needed by the application
// exists on the filesystem. It creates:
//   - The main configuration directory (ConfigDir)
//   - The models storage directory (ModelsDir)
//
// Directories are created with 0755 permissions (rwxr-xr-x), allowing
// the owner full access and read/execute access for group and others.
//
// Returns:
//   - nil if all directories were created successfully or already exist
//   - error if any directory creation fails
//
// Example:
//
//	if err := cfg.EnsureDirectories(); err != nil {
//	    log.Fatalf("Failed to create directories: %v", err)
//	}
func (c *Config) EnsureDirectories() error {
	dirs := []string{
		c.Storage.ConfigDir,
		c.Storage.ModelsDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// WriteServerConfig writes the server configuration to a file.
//
// This method saves the current server information to a JSON configuration file
// that can be automatically discovered by clients. This eliminates the
// need for clients to specify --server for local connections.
//
// The configuration is written to ~/.xw/server.json and contains the
// server's HTTP address, PID, start time, and version.
//
// Returns:
//   - nil if the configuration is written successfully
//   - error if the write operation fails
//
// Example:
//
//	if err := cfg.WriteServerConfig(); err != nil {
//	    logger.Warn("Failed to write server config: %v", err)
//	}
func (c *Config) WriteServerConfig() error {
	configFile := filepath.Join(c.Storage.ConfigDir, ServerConfigFile)
	
	// Ensure directory exists
	if err := os.MkdirAll(c.Storage.ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	
	// Create server info
	info := ServerInfo{
		Address:   c.GetServerAddress(),
		PID:       os.Getpid(),
		StartTime: time.Now().Format(time.RFC3339),
		Version:   "1.0.0", // TODO: Get from build-time variable
	}
	
	// Marshal to JSON with indentation for readability
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal server config: %w", err)
	}
	
	// Write to file
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write server config: %w", err)
	}
	
	return nil
}

// ReadServerConfig reads the server configuration from a file.
//
// This method attempts to read the server information from the JSON configuration
// file created by a running server. If the file doesn't exist or is
// unreadable, it returns an empty string and no error.
//
// Clients should use this method to auto-discover the server address
// before falling back to defaults or command-line arguments.
//
// Returns:
//   - The server address if found, empty string otherwise
//   - error only if there's a read error (not if file doesn't exist)
//
// Example:
//
//	cfg := config.NewDefaultConfig()
//	if addr, _ := cfg.ReadServerConfig(); addr != "" {
//	    // Use discovered address
//	    client := client.NewClient(addr)
//	}
func (c *Config) ReadServerConfig() (string, error) {
	configFile := filepath.Join(c.Storage.ConfigDir, ServerConfigFile)
	
	// Check if file exists
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return "", nil // Not an error, just not found
	}
	
	// Read server config file
	data, err := os.ReadFile(configFile)
	if err != nil {
		return "", fmt.Errorf("failed to read server config: %w", err)
	}
	
	// Parse JSON
	var info ServerInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return "", fmt.Errorf("failed to parse server config: %w", err)
	}
	
	return info.Address, nil
}

// RemoveServerConfig removes the server configuration file.
//
// This method should be called when the server is shutting down to
// clean up the configuration file and prevent clients from attempting
// to connect to a non-existent server.
//
// Returns:
//   - nil if the file is removed successfully or doesn't exist
//   - error if the removal fails
//
// Example:
//
//	defer func() {
//	    if err := cfg.RemoveServerConfig(); err != nil {
//	        logger.Warn("Failed to remove server config: %v", err)
//	    }
//	}()
func (c *Config) RemoveServerConfig() error {
	configFile := filepath.Join(c.Storage.ConfigDir, ServerConfigFile)
	
	// Remove file, ignore if it doesn't exist
	if err := os.Remove(configFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove server config: %w", err)
	}

	return nil
}
