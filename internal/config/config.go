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
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultServerHost is the default server host address.
	// The server listens on localhost by default for security.
	DefaultServerHost = "localhost"

	// DefaultServerPort is the default server port.
	// Port 11581 is used as it doesn't require root privileges.
	DefaultServerPort = 11581

	// DefaultRegistry is the default configuration package registry URL.
	DefaultRegistry = "https://xw.tsingmao.com/packages.json"

	// DefaultConfigDirName is the default configuration directory name.
	// This directory is created in the user's home directory.
	DefaultConfigDirName = ".xw"

	// DefaultDataDirName is the default data directory name.
	// This subdirectory under config dir contains all runtime data.
	DefaultDataDirName = "data"

	// DefaultModelsDir is the default models directory name.
	// Model files are stored in this subdirectory within the data directory.
	DefaultModelsDir = "models"
)

// Config represents the complete application configuration.
//
// This is the root configuration struct that contains all settings
// required for running the xw application, including server and storage
// configurations. The struct can be serialized to/from JSON for persistence.
type Config struct {
	// Server holds the HTTP server configuration including host and port.
	Server ServerConfig `json:"server"`

	// Storage holds the storage configuration including directories for
	// data and configuration files.
	Storage StorageConfig `json:"storage"`
	
	// RuntimeParams holds runtime parameter templates loaded at startup.
	RuntimeParams *RuntimeParamsConfig `json:"-"`
	
	// BinaryVersion is the version of the xw binary (e.g., "v0.0.1").
	// Set from main.Version during initialization.
	// Used as the default config_version if not specified in server.conf.
	BinaryVersion string `json:"-"`
}

// ServerConfig represents the HTTP server configuration.
//
// This configuration controls how the xw server listens for incoming
// HTTP connections from CLI clients or other API consumers.
type ServerConfig struct {
	// Name is the unique identifier for this server instance.
	Name string `json:"name"`

	// Registry is the configuration package registry URL.
	Registry string `json:"registry"`

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
// This configuration defines where the application stores its data
// and configuration files.
type StorageConfig struct {
	// ConfigDir is the absolute path to the configuration files directory.
	// Contains YAML configuration files like devices.yaml, models.yaml.
	// Example: "/home/user/.xw"
	ConfigDir string `json:"config_dir"`

	// DataDir is the absolute path to the main data directory.
	// Contains all runtime data including models and server state.
	// Example: "/home/user/.xw/data"
	DataDir string `json:"data_dir"`
}

// GetModelsDir returns the models storage directory path.
// Models are stored in a "models" subdirectory within the data directory.
// Example: ~/.xw/data/models
func (s *StorageConfig) GetModelsDir() string {
	return filepath.Join(s.DataDir, DefaultModelsDir)
}

// NewDefaultConfig creates a new configuration instance with default values.
//
// This function initializes a Config struct with sensible defaults suitable
// for user-level deployment. The configuration uses:
//   - Server: localhost:11581 for local-only access
//   - ConfigDir: ~/.xw for configuration files (devices.yaml, models.yaml)
//   - DataDir: ~/.xw/data for runtime data and models
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

	configDir := filepath.Join(homeDir, DefaultConfigDirName)
	dataDir := filepath.Join(configDir, DefaultDataDirName)

	return &Config{
		Server: ServerConfig{
			Host:    DefaultServerHost,
			Port:    DefaultServerPort,
			Address: fmt.Sprintf("http://%s:%d", DefaultServerHost, DefaultServerPort),
		},
		Storage: StorageConfig{
			ConfigDir: configDir,
			DataDir:   dataDir,
		},
	}
}

// NewConfigWithCustomDirs creates a new configuration with custom directories.
//
// This function allows specifying custom configuration and data directories
// instead of using the defaults. Useful for:
//   - Testing with isolated environments
//   - Running multiple instances
//   - Custom deployment scenarios
//
// Parameters:
//   - configDir: Custom configuration files directory path (empty string uses default ~/.xw)
//   - dataDir: Custom data directory path (empty string uses configDir/data)
//
// Returns:
//   - A pointer to a newly created Config with the specified directories
//
// Example:
//
//	cfg := config.NewConfigWithCustomDirs("/opt/xw", "")
//	// Config files: /opt/xw/*.yaml
//	// Data directory: /opt/xw/data
//	// Models will be stored in /opt/xw/data/models/
func NewConfigWithCustomDirs(configDir, dataDir string) *Config {
	// If no configDir specified, use default
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = "/tmp"
		}
		configDir = filepath.Join(homeDir, DefaultConfigDirName)
	}
	
	// If no dataDir specified, use configDir/data
	if dataDir == "" {
		dataDir = filepath.Join(configDir, DefaultDataDirName)
	}

	return &Config{
		Server: ServerConfig{
			Host:    DefaultServerHost,
			Port:    DefaultServerPort,
			Address: fmt.Sprintf("http://%s:%d", DefaultServerHost, DefaultServerPort),
		},
		Storage: StorageConfig{
			ConfigDir: configDir,
			DataDir:   dataDir,
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
		c.Storage.DataDir,
		c.Storage.GetModelsDir(),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// LoadVersionedConfigs loads all configuration files at startup.
//
// This method loads the three main configuration files from the versioned
// config directory at startup time:
//   - runtime_params.yaml: Runtime parameter templates (stored in Config.RuntimeParams)
//   - devices.yaml: Device and runtime images config (cached globally)
//   - models.yaml: Model definitions (registered globally via loadModels callback)
//
// After this call, all configurations are loaded and ready to use throughout
// the application lifecycle. No further path handling or file loading needed.
//
// Parameters:
//   - configVersion: Configuration version string (e.g., "v0.0.1")
//   - loadModels: Callback function to load and register models from models.yaml
//
// Returns:
//   - error if any configuration file fails to load
//
// Example:
//
//	err := cfg.LoadVersionedConfigs("v0.0.1", server.InitializeModels)
func (c *Config) LoadVersionedConfigs(configVersion string, loadModels func(string) error) error {
	versionedDir := filepath.Join(c.Storage.ConfigDir, configVersion)
	
	// Load runtime_params.yaml and store in Config
	runtimeParamsPath := filepath.Join(versionedDir, "runtime_params.yaml")
	runtimeParams, err := LoadRuntimeParamsConfigFrom(runtimeParamsPath)
	if err != nil {
		return fmt.Errorf("failed to load runtime_params.yaml: %w", err)
	}
	c.RuntimeParams = runtimeParams
	
	// Load devices.yaml (internally cached globally)
	devicesPath := filepath.Join(versionedDir, "devices.yaml")
	if _, err := LoadRuntimeImagesConfigFrom(devicesPath); err != nil {
		return fmt.Errorf("failed to load devices.yaml: %w", err)
	}
	
	// Load models.yaml (registered globally via callback)
	modelsPath := filepath.Join(versionedDir, "models.yaml")
	if err := loadModels(modelsPath); err != nil {
		return fmt.Errorf("failed to load models.yaml: %w", err)
	}
	
	return nil
}

// ReloadVersionedConfigs reloads all configuration files without server restart.
//
// This method implements transactional reload with validation phase:
// 1. All configurations are loaded and validated without modifying caches
// 2. Only if all validations succeed, caches are updated atomically
// 3. If any configuration fails, no changes are applied
//
// Configuration files reloaded:
//   - runtime_params.yaml: Runtime parameter templates (stored in Config.RuntimeParams)
//   - devices.yaml: Device and runtime images config (cache cleared and reloaded)
//   - models.yaml: Model definitions (re-registered, overwrites existing)
//
// This method should be called when configuration needs to be updated without
// restarting the server (e.g., after running 'xw update').
//
// Parameters:
//   - configVersion: Configuration version string (e.g., "v0.0.1")
//   - loadModels: Callback function to load and register models from models.yaml
//
// Returns:
//   - error if any configuration file fails to reload (no changes applied)
//
// Example:
//
//	err := cfg.ReloadVersionedConfigs("v0.0.1", server.InitializeModels)
func (c *Config) ReloadVersionedConfigs(configVersion string, loadModels func(string) error) error {
	versionedDir := filepath.Join(c.Storage.ConfigDir, configVersion)
	
	// Phase 1: Load and validate all configurations (no cache modifications)
	
	// Load and validate runtime_params.yaml
	runtimeParamsPath := filepath.Join(versionedDir, "runtime_params.yaml")
	newRuntimeParams, err := LoadRuntimeParamsConfigFrom(runtimeParamsPath)
	if err != nil {
		return fmt.Errorf("failed to load runtime_params.yaml: %w", err)
	}
	
	// Validate devices.yaml by parsing without updating cache
	devicesPath := filepath.Join(versionedDir, "devices.yaml")
	if err := validateDevicesFile(devicesPath); err != nil {
		return fmt.Errorf("failed to validate devices.yaml: %w", err)
	}
	
	// Validate models.yaml by parsing without registering
	modelsPath := filepath.Join(versionedDir, "models.yaml")
	if err := validateModelsFile(modelsPath); err != nil {
		return fmt.Errorf("failed to validate models.yaml: %w", err)
	}
	
	// Phase 2: All validations passed, commit changes atomically
	
	// Update runtime params in Config
	c.RuntimeParams = newRuntimeParams
	
	// Reload devices config (clears cache and reloads from file)
	if _, err := ReloadDevicesConfig(devicesPath); err != nil {
		return fmt.Errorf("failed to reload devices.yaml: %w", err)
	}
	
	// Clear models cache before reloading
	ClearModelsConfigCache()
	
	// Reload models (re-registers all models, overwrites existing)
	if err := loadModels(modelsPath); err != nil { 
		return fmt.Errorf("failed to reload models.yaml: %w", err)
	}
	
	return nil
}

// validateDevicesFile validates devices.yaml without updating cache.
func validateDevicesFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}
	
	var config DevicesConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}
	
	return validateDevicesConfig(&config)
}

// validateModelsFile validates models.yaml without registering models.
func validateModelsFile(path string) error {
	modConfig, err := LoadModelsConfig(path)
	if err != nil {
		return fmt.Errorf("failed to load models config: %w", err)
	}
	
	if len(modConfig.Models) == 0 {
		return fmt.Errorf("no models defined in configuration")
	}
	
	return nil
}
