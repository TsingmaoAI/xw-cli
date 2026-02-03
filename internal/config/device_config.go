// Package config - device_config.go implements device configuration loading and management.
//
// This module provides a flexible configuration system for AI chip detection and management.
// Device configurations are loaded from YAML files, allowing easy addition of new chip
// types without code changes.
//
// The configuration system supports multiple bus types (PCIe, CXL, virtual) and provides
// a clean abstraction for chip vendor and model definitions.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
	
	"github.com/tsingmaoai/xw-cli/internal/logger"
)


// ChipModelConfig defines configuration for a specific chip model.
//
// Each chip model has unique characteristics that affect how xw manages it:
//   - Hardware identification (for device detection)
//   - Human-readable names (for display and logging)
//   - Capabilities (for compatibility checks)
//   - Generation info (for version-specific handling)
//   - Runtime images (Docker images for inference engines)
type ChipModelConfig struct {
	// ConfigKey is the unique identifier used in runtime configuration
	// This key maps to runtime images and deployment settings
	// Example: "ascend-910b", "ascend-310p"
	ConfigKey string `yaml:"config_key"`
	
	// ModelName is the human-readable chip model name
	// Example: "Ascend 910B", "Ascend 310P"
	ModelName string `yaml:"model_name"`
	
	// DeviceID is the PCIe device identifier (16-bit hex value)
	// Example: "0xd802" for Ascend 910B
	DeviceID string `yaml:"device_id"`
	
	// Generation groups related chip models (optional)
	// Example: "Ascend 9xx", "Ascend 3xx"
	Generation string `yaml:"generation,omitempty"`
	
	// Capabilities lists supported features and precision modes
	// Example: ["int8", "fp16", "inference"]
	Capabilities []string `yaml:"capabilities,omitempty"`
	
	// ChipsPerDevice specifies how many AI chips are on each physical PCI device
	// Default: 1 (single-chip card)
	// Example: 2 for dual-chip cards like Ascend 910B Duo
	// This allows proper device enumeration where one PCI device contains multiple inference cores
	ChipsPerDevice int `yaml:"chips_per_device,omitempty"`
	
	// Topology defines the physical topology for this chip model
	// Used for topology-aware allocation specific to this chip type
	Topology *TopologyConfig `yaml:"topology,omitempty"`
	
	// RuntimeImages maps inference engines to their Docker images by architecture
	// Structure: engine_name -> architecture -> image_url
	// Example: {"vllm": {"arm64": "quay.io/...", "amd64": "..."}}
	RuntimeImages map[string]map[string]string `yaml:"runtime_images,omitempty"`
}

// ChipVendorConfig defines configuration for a chip vendor.
//
// Vendors are manufacturers of AI accelerator chips. Each vendor may produce
// multiple chip families and models.
type ChipVendorConfig struct {
	// VendorName is the company/organization name
	// Example: "Huawei", "Baidu", "Cambricon"
	VendorName string `yaml:"vendor_name"`
	
	// VendorID is the PCIe vendor identifier (16-bit hex value)
	// Example: "0x19e5" for Huawei
	VendorID string `yaml:"vendor_id"`
	
	// ChipModels lists all chip models from this vendor
	ChipModels []ChipModelConfig `yaml:"chip_models"`
}

// TopologyBox represents a group of devices with high-speed interconnection.
//
// Devices within the same box are considered to have zero distance for
// allocation optimization. Devices in different boxes have a distance
// equal to the absolute difference of their box indices.
type TopologyBox struct {
	// Devices is a list of logical chip indices in this box
	// Example: [0, 1, 2, 3] means logical chips 0-3 have high-speed interconnect
	// Note: Use logical chip indices, NOT physical device indices
	Devices []int `yaml:"devices"`
}

// TopologyConfig defines the physical topology of devices.
//
// This configuration enables topology-aware device allocation, ensuring
// that allocated devices are as close as possible in the physical topology.
type TopologyConfig struct {
	// Boxes is a list of device groups with high-speed interconnection
	// Each box contains devices with zero intra-box distance
	// Inter-box distance equals |box_index_a - box_index_b|
	Boxes []TopologyBox `yaml:"boxes,omitempty"`
}

// DevicesConfig is the root configuration structure for device definitions.
//
// This structure maps to the YAML configuration file and contains all
// vendor and chip model definitions.
type DevicesConfig struct {
	// Version specifies the configuration schema version
	// Used for compatibility checking and migration
	Version string `yaml:"version"`
	
	// Vendors contains all supported chip vendors and their models
	// Each vendor's chip models can define their own topology configuration
	Vendors []ChipVendorConfig `yaml:"vendors"`
}

// DeviceConfigLoader handles loading and caching of device configurations.
//
// The loader implements singleton pattern with lazy initialization.
// Configurations are loaded once and cached for the lifetime of the application.
//
// Thread Safety: All methods are safe for concurrent use.
type DeviceConfigLoader struct {
	mu     sync.RWMutex
	config *DevicesConfig
	loaded bool
}

var (
	// deviceConfigLoader is the global singleton instance
	deviceConfigLoader = &DeviceConfigLoader{}
	
	// defaultDeviceConfigPath is the default location for device configuration
	defaultDeviceConfigPath = "/etc/xw/devices.yaml"
)

// LoadDevicesConfig loads device configuration from the default location.
//
// This function loads the configuration from the default path: /etc/xw/devices.yaml
// It implements a singleton pattern with caching.
//
// Returns:
//   - Pointer to loaded DevicesConfig
//   - Error if file cannot be read, parsed, or validated
//
// Example:
//
//	config, err := LoadDevicesConfig()
//	if err != nil {
//	    log.Fatalf("Failed to load device config: %v", err)
//	}
//	for _, vendor := range config.Vendors {
//	    fmt.Printf("Loaded vendor: %s\n", vendor.VendorName)
//	}
func LoadDevicesConfig() (*DevicesConfig, error) {
	return LoadDevicesConfigFrom("")
}

// LoadDevicesConfigFrom loads device configuration from a specified path.
//
// This function is used when you need to load configuration from a specific
// file path instead of the default location. It implements a singleton pattern
// with caching.
//
// Parameters:
//   - configPath: Path to configuration file (empty string for default)
//
// Returns:
//   - Pointer to loaded DevicesConfig
//   - Error if file cannot be read, parsed, or validated
//
// Example:
//
//	config, err := LoadDevicesConfigFrom("/custom/path/devices.yaml")
//	if err != nil {
//	    log.Fatalf("Failed to load device config: %v", err)
//	}
func LoadDevicesConfigFrom(configPath string) (*DevicesConfig, error) {
	deviceConfigLoader.mu.Lock()
	defer deviceConfigLoader.mu.Unlock()
	
	// Return cached config if already loaded
	if deviceConfigLoader.loaded {
		logger.Debug("Using cached device configuration")
		return deviceConfigLoader.config, nil
	}
	
	// Determine config file path
	path := configPath
	if path == "" {
		path = defaultDeviceConfigPath
		logger.Debug("Using default device config path: %s", path)
	}
	
	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("device configuration file not found: %s", path)
	}
	
	// Read configuration file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read device config file %s: %w", path, err)
	}
	
	// Parse YAML
	var config DevicesConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse device config YAML: %w", err)
	}
	
	// Validate configuration
	if err := validateDevicesConfig(&config); err != nil {
		return nil, fmt.Errorf("invalid device configuration: %w", err)
	}
	
	// Cache the loaded configuration
	deviceConfigLoader.config = &config
	deviceConfigLoader.loaded = true
	
	logger.Debug("Loaded device configuration: %d vendor(s), %d chip model(s)", 
		len(config.Vendors), countChipModels(&config))
	
	return &config, nil
}

// GetDevicesConfig returns the cached device configuration.
//
// This method provides access to the previously loaded configuration without
// re-reading the file. If configuration hasn't been loaded yet, it loads it
// from the default location.
//
// Returns:
//   - Pointer to DevicesConfig
//   - Error if configuration not loaded and loading fails
func GetDevicesConfig() (*DevicesConfig, error) {
	deviceConfigLoader.mu.RLock()
	if deviceConfigLoader.loaded {
		config := deviceConfigLoader.config
		deviceConfigLoader.mu.RUnlock()
		return config, nil
	}
	deviceConfigLoader.mu.RUnlock()
	
	// Not loaded yet, load with default path
	return LoadDevicesConfig()
}

// ReloadDevicesConfig forces a reload of the device configuration.
//
// This method clears the cache and re-reads the configuration file.
// Useful for applying configuration changes without restarting the application.
//
// Parameters:
//   - configPath: Optional path to configuration file (empty for default)
//
// Returns:
//   - Pointer to reloaded DevicesConfig
//   - Error if reload fails
func ReloadDevicesConfig(configPath string) (*DevicesConfig, error) {
	deviceConfigLoader.mu.Lock()
	deviceConfigLoader.loaded = false
	deviceConfigLoader.config = nil
	deviceConfigLoader.mu.Unlock()
	
	logger.Info("Reloading device configuration")
	return LoadDevicesConfigFrom(configPath)
}

// validateDevicesConfig performs validation on the loaded configuration.
//
// Validation checks:
//   - Version field is present
//   - At least one vendor is defined
//   - Each vendor has valid identifiers
//   - Each chip model has required fields
//   - No duplicate config keys
//
// Parameters:
//   - config: Configuration to validate
//
// Returns:
//   - nil if valid
//   - Error describing validation failure
func validateDevicesConfig(config *DevicesConfig) error {
	if config.Version == "" {
		return fmt.Errorf("configuration version is required")
	}
	
	if len(config.Vendors) == 0 {
		return fmt.Errorf("at least one vendor must be defined")
	}
	
	// Track config keys to detect duplicates
	configKeys := make(map[string]bool)
	
	for i, vendor := range config.Vendors {
		if vendor.VendorName == "" {
			return fmt.Errorf("vendor[%d]: vendor_name is required", i)
		}
		
		if vendor.VendorID == "" {
			return fmt.Errorf("vendor %s: vendor_id is required", vendor.VendorName)
		}
		
		if len(vendor.ChipModels) == 0 {
			return fmt.Errorf("vendor %s: at least one chip model must be defined", vendor.VendorName)
		}
		
		for j, model := range vendor.ChipModels {
			if model.ConfigKey == "" {
				return fmt.Errorf("vendor %s, model[%d]: config_key is required", vendor.VendorName, j)
			}
			
			if model.ModelName == "" {
				return fmt.Errorf("vendor %s, model %s: model_name is required", vendor.VendorName, model.ConfigKey)
			}
			
			// Check for duplicate config keys
			if configKeys[model.ConfigKey] {
				return fmt.Errorf("duplicate config_key: %s", model.ConfigKey)
			}
			configKeys[model.ConfigKey] = true
			
			if model.DeviceID == "" {
				return fmt.Errorf("vendor %s, model %s: device_id is required", 
					vendor.VendorName, model.ConfigKey)
			}
		}
	}
	
	return nil
}

// countChipModels returns the total number of chip models across all vendors.
func countChipModels(config *DevicesConfig) int {
	count := 0
	for _, vendor := range config.Vendors {
		count += len(vendor.ChipModels)
	}
	return count
}

// FindChipModelByConfigKey searches for a chip model by its config key.
//
// This is a convenience method for looking up chip configuration by the
// unique config_key identifier.
//
// Parameters:
//   - config: DevicesConfig to search
//   - configKey: The config_key to find
//
// Returns:
//   - Pointer to ChipModelConfig if found
//   - nil if not found
func FindChipModelByConfigKey(config *DevicesConfig, configKey string) *ChipModelConfig {
	for _, vendor := range config.Vendors {
		for i := range vendor.ChipModels {
			if vendor.ChipModels[i].ConfigKey == configKey {
				return &vendor.ChipModels[i]
			}
		}
	}
	return nil
}

// FindChipModelByIdentifier searches for a chip model by PCIe hardware identifier.
//
// This method is used during device detection to match discovered hardware
// against known chip models.
//
// Parameters:
//   - config: DevicesConfig to search
//   - vendorID: PCIe vendor identifier (e.g., "0x19e5")
//   - deviceID: PCIe device identifier (e.g., "0xd802")
//
// Returns:
//   - Pointer to ChipModelConfig if found
//   - Pointer to ChipVendorConfig for the vendor if found
//   - nil, nil if not found
func FindChipModelByIdentifier(config *DevicesConfig, vendorID, deviceID string) (*ChipVendorConfig, *ChipModelConfig) {
	for i := range config.Vendors {
		if config.Vendors[i].VendorID != vendorID {
			continue
		}
		// Found matching vendor, now search for device
		for j := range config.Vendors[i].ChipModels {
			if config.Vendors[i].ChipModels[j].DeviceID == deviceID {
				// Found matching chip model
				return &config.Vendors[i], &config.Vendors[i].ChipModels[j]
			}
		}
	}
	return nil, nil
}

// GetAllConfigKeys returns a list of all config keys defined in the configuration.
//
// Useful for validation and displaying available chip types.
//
// Parameters:
//   - config: DevicesConfig to extract keys from
//
// Returns:
//   - Slice of config key strings
func GetAllConfigKeys(config *DevicesConfig) []string {
	var keys []string
	for _, vendor := range config.Vendors {
		for _, model := range vendor.ChipModels {
			keys = append(keys, model.ConfigKey)
		}
	}
	return keys
}

// SaveDevicesConfig writes a DevicesConfig to a YAML file.
//
// This method is primarily used for:
//   - Generating template configuration files
//   - Exporting modified configurations
//   - Testing and validation
//
// Parameters:
//   - config: Configuration to save
//   - path: File path to write to
//
// Returns:
//   - Error if file cannot be written
func SaveDevicesConfig(config *DevicesConfig, path string) error {
	// Validate before saving
	if err := validateDevicesConfig(config); err != nil {
		return fmt.Errorf("cannot save invalid configuration: %w", err)
	}
	
	// Marshal to YAML with nice formatting
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config to YAML: %w", err)
	}
	
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	
	// Write file
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", path, err)
	}
	
	logger.Info("Saved device configuration to %s", path)
	return nil
}

