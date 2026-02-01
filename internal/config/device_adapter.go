// Package config - device_adapter.go provides adapters for device configuration.
//
// This module converts device configuration into formats used by the device
// manager, avoiding circular dependencies between config and device packages.
package config

import (
	"fmt"
	
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/logger"
)

// ChipModelInfo represents chip model information in a format usable by device package.
//
// This structure avoids direct dependency on device package types, breaking
// the circular dependency between config and device packages.
type ChipModelInfo struct {
	VendorID     string
	DeviceID     string
	ModelName    string
	ConfigKey    string
	DeviceType   api.DeviceType
	Generation   string
	Capabilities []string
}

// LoadChipModels loads chip models from the device configuration.
//
// This function reads the device configuration and converts it into a simple
// structure that can be easily consumed by the device detection code.
//
// Returns:
//   - Slice of chip model information
//   - Error if configuration cannot be loaded or is invalid
//
// Example:
//
//	models, err := LoadChipModels()
//	if err != nil {
//	    log.Fatalf("Failed to load chip models: %v", err)
//	}
//	for _, model := range models {
//	    fmt.Printf("Loaded: %s (%s)\n", model.ModelName, model.ConfigKey)
//	}
func LoadChipModels() ([]ChipModelInfo, error) {
	// Load device configuration
	devConfig, err := LoadDevicesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load device configuration: %w", err)
	}
	
	// Convert configuration to ChipModelInfo format
	var chipModels []ChipModelInfo
	
	for _, vendor := range devConfig.Vendors {
		for _, model := range vendor.ChipModels {
			chipModel := ChipModelInfo{
				VendorID:     vendor.VendorID,
				DeviceID:     model.DeviceID,
				ModelName:    model.ModelName,
				ConfigKey:    model.ConfigKey,
				DeviceType:   api.DeviceType(model.ConfigKey),
				Generation:   model.Generation,
				Capabilities: model.Capabilities,
			}
			chipModels = append(chipModels, chipModel)
		}
	}
	
	logger.Debug("Loaded %d chip model(s) from configuration", len(chipModels))
	return chipModels, nil
}

// GetSupportedDeviceTypes returns all device types from device configuration.
//
// This function reads the device configuration and extracts all config_key
// values, which represent the supported device types.
//
// Returns:
//   - Slice of DeviceType values from configuration
//   - Error if configuration cannot be loaded
//
// Example:
//
//	types, err := GetSupportedDeviceTypes()
//	if err != nil {
//	    log.Fatalf("Failed to get device types: %v", err)
//	}
//	fmt.Printf("Supported device types: %v\n", types)
func GetSupportedDeviceTypes() ([]api.DeviceType, error) {
	// Load device configuration
	devConfig, err := LoadDevicesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load device configuration: %w", err)
	}
	
	// Extract all config keys as device types
	var deviceTypes []api.DeviceType
	for _, vendor := range devConfig.Vendors {
		for _, model := range vendor.ChipModels {
			deviceTypes = append(deviceTypes, api.DeviceType(model.ConfigKey))
		}
	}
	
	return deviceTypes, nil
}

// LookupChipModelByPCIID looks up a chip model by PCIe vendor and device IDs.
//
// This function searches the device configuration for a chip model matching
// the specified PCIe identifiers. It's used during hardware detection to
// identify discovered devices.
//
// Parameters:
//   - vendorID: PCIe vendor ID (e.g., "0x19e5")
//   - deviceID: PCIe device ID (e.g., "0xd802")
//
// Returns:
//   - Pointer to ChipModelInfo if found
//   - nil if not found
//   - Error if configuration cannot be loaded
//
// Example:
//
//	model, err := LookupChipModelByPCIID("0x19e5", "0xd802")
//	if err != nil {
//	    log.Fatalf("Lookup failed: %v", err)
//	}
//	if model != nil {
//	    fmt.Printf("Found: %s\n", model.ModelName)
//	}
func LookupChipModelByPCIID(vendorID, deviceID string) (*ChipModelInfo, error) {
	// Load device configuration
	devConfig, err := LoadDevicesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load device configuration: %w", err)
	}
	
	// Search for matching chip model
	vendor, model := FindChipModelByIdentifier(devConfig, vendorID, deviceID)
	if vendor == nil || model == nil {
		// Not found
		return nil, nil
	}
	
	// Convert to ChipModelInfo format
	chipModel := &ChipModelInfo{
		VendorID:     vendor.VendorID,
		DeviceID:     model.DeviceID,
		ModelName:    model.ModelName,
		ConfigKey:    model.ConfigKey,
		DeviceType:   api.DeviceType(model.ConfigKey),
		Generation:   model.Generation,
		Capabilities: model.Capabilities,
	}
	
	return chipModel, nil
}

// GetConfigKeyByModelName returns the configuration key for a chip model name.
//
// This function looks up the ConfigKey for a given ModelName from the device configuration.
// The ConfigKey is used in runtime configuration files (e.g., runtime_images.yaml).
//
// Parameters:
//   - modelName: Human-readable chip model name (e.g., "Ascend 910B", "Ascend 310P")
//
// Returns:
//   - Configuration key (e.g., "ascend-910b", "ascend-310p")
//   - Error if the model name is not found or configuration loading fails
//
// Example:
//
//	configKey, err := GetConfigKeyByModelName("Ascend 310P")
//	// Returns: "ascend-310p", nil
func GetConfigKeyByModelName(modelName string) (string, error) {
	// Load device configuration
	devConfig, err := LoadDevicesConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load device configuration: %w", err)
	}
	
	// Search for matching model name
	for _, vendor := range devConfig.Vendors {
		for _, model := range vendor.ChipModels {
			if model.ModelName == modelName {
				if model.ConfigKey == "" {
					return "", fmt.Errorf("chip model %s has no config key defined", modelName)
				}
				return model.ConfigKey, nil
			}
		}
	}
	
	return "", fmt.Errorf("unknown chip model: %s", modelName)
}

