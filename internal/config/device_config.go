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


// ChipVariant defines a specific variant of a chip model.
//
// Variants are used to distinguish between different sub-versions of the same chip
// that share the same device_id but have different subsystem_device_id.
// Example: 910B1, 910B2, 910B3 are variants of 910B
// Note: All variants of the same chip model use the same runtime images from base config
type ChipVariant struct {
	// SubsystemDeviceID is the PCIe subsystem device identifier (16-bit hex value)
	// Example: "0x0001" for 910B1, "0x0002" for 910B2
	SubsystemDeviceID string `yaml:"subsystem_device_id"`
	
	// VariantKey is the unique identifier for this variant
	// Example: "ascend-910b1", "ascend-910b2"
	VariantKey string `yaml:"variant_key"`
	
	// VariantName is the human-readable variant name
	// Example: "910B1", "910B2"
	VariantName string `yaml:"variant_name,omitempty"`
}

// ChipModelConfig defines configuration for a specific chip model.
//
// Each chip model has unique characteristics that affect how xw manages it:
//   - Hardware identification (for device detection)
//   - Human-readable names (for display and logging)
//   - Capabilities (for compatibility checks)
//   - Generation info (for version-specific handling)
//   - Runtime images (Docker images for inference engines)
//   - Variants (optional sub-versions with different subsystem_device_id)
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
	
	// SubsystemDeviceID is the PCIe subsystem device identifier (16-bit hex value, optional)
	// DEPRECATED: Use Variants instead for chip sub-versions
	// When empty, matching is based on VendorID and DeviceID only
	SubsystemDeviceID string `yaml:"subsystem_device_id,omitempty"`
	
	// Variants defines a list of chip sub-versions with different subsystem_device_id
	// Used to distinguish between models like 910B1, 910B2, etc.
	// If hardware matches a variant, the variant's config_key is used
	// All variants share the same runtime_images from base model config
	// If no variant matches, the base model config is used as fallback
	Variants []ChipVariant `yaml:"variants,omitempty"`
	
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
	// All variants of this chip model share the same runtime images
	RuntimeImages map[string]map[string]string `yaml:"runtime_images,omitempty"`
	
	// ExtSandboxes defines configuration-based sandboxes for this chip model (optional)
	// Structure: engine_name -> sandbox_config
	// Example: {"vllm": {device_env: "XPU_VISIBLE_DEVICES", devices: [...], ...}}
	// This enables support for niche chips without code-based sandbox implementations
	// If defined, runtime engines will automatically register these sandboxes during initialization
	ExtSandboxes map[string]*ExtSandboxConfig `yaml:"ext_sandboxes,omitempty"`
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
// base model's config_key identifier. It does NOT search variant keys.
//
// Parameters:
//   - config: DevicesConfig to search
//   - configKey: The base model config_key to find (e.g., "ascend-910b")
//
// Returns:
//   - Pointer to ChipModelConfig if found (base model)
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
// against known chip models. It supports variant matching for chip sub-versions.
//
// Three-phase matching logic (优雅降级):
//   Phase 1 (Variant match): If model has variants, try to match subsystem_device_id
//     - Returns base model + matched variant
//   Phase 2 (Legacy exact match): Try configs WITH subsystem_device_id (deprecated)
//     - For backward compatibility with old config format
//   Phase 3 (Fallback): Match configs WITHOUT subsystem_device_id or variants
//     - Acts as generic fallback for unknown variants
//
// Configuration example (new format with variants):
//   - config_key: ascend-910b
//     device_id: "0xd802"
//     variants:
//       - subsystem_device_id: "0x0001"
//         variant_key: "ascend-910b1"
//         variant_name: "910B1"
//       - subsystem_device_id: "0x0002"
//         variant_key: "ascend-910b2"
//         variant_name: "910B2"
//     # Base config acts as fallback if no variant matches
//
// Parameters:
//   - config: DevicesConfig to search
//   - vendorID: PCIe vendor identifier (e.g., "0x19e5")
//   - deviceID: PCIe device identifier (e.g., "0xd802")
//   - subsystemDeviceID: PCIe subsystem device identifier (e.g., "0x0001"), can be empty
//
// Returns:
//   - Pointer to ChipVendorConfig for the vendor if found
//   - Pointer to ChipModelConfig (base model) if found
//   - Pointer to ChipVariant if a variant matched (nil if using base config)
//   - All nil if not found
func FindChipModelByIdentifier(config *DevicesConfig, vendorID, deviceID, subsystemDeviceID string) (*ChipVendorConfig, *ChipModelConfig, *ChipVariant) {
	var fallbackVendor *ChipVendorConfig
	var fallbackModel *ChipModelConfig
	
	for i := range config.Vendors {
		if config.Vendors[i].VendorID != vendorID {
			continue
		}
		
		// Found matching vendor, now search for device with three-phase approach
		for j := range config.Vendors[i].ChipModels {
			model := &config.Vendors[i].ChipModels[j]
			
			// Device ID must match
			if model.DeviceID != deviceID {
				continue
			}
			
			// Phase 1: Try to match variants (new format)
			if len(model.Variants) > 0 && subsystemDeviceID != "" {
				for k := range model.Variants {
					variant := &model.Variants[k]
					if variant.SubsystemDeviceID == subsystemDeviceID {
						// Variant matched! Return base model + variant
						return &config.Vendors[i], model, variant
					}
				}
				// Has variants but none matched, store as potential fallback
				if fallbackVendor == nil {
					fallbackVendor = &config.Vendors[i]
					fallbackModel = model
				}
				continue
			}
			
			// Phase 2: Legacy exact match - config has subsystem_device_id (deprecated format)
			if model.SubsystemDeviceID != "" {
				if model.SubsystemDeviceID == subsystemDeviceID {
					// Exact match found! Return immediately (no variant)
					return &config.Vendors[i], model, nil
				}
				// Config has subsystem_device_id but doesn't match, continue searching
				continue
			}
			
			// Phase 3: Potential fallback - config has no subsystem_device_id or variants
			// Store as fallback but continue searching for exact match
			if fallbackVendor == nil {
				fallbackVendor = &config.Vendors[i]
				fallbackModel = model
			}
		}
	}
	
	// Return fallback if no exact match was found
	return fallbackVendor, fallbackModel, nil
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

