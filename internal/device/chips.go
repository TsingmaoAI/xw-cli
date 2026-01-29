// Package device provides domestic chip device detection and management.
package device

import (
	"fmt"
	
	"github.com/tsingmao/xw/internal/api"
)

// ChipVendor represents a chip vendor's PCI vendor ID and name
type ChipVendor struct {
	// VendorID is the PCI vendor ID (e.g., "0x1db7" for Baidu)
	VendorID string
	
	// VendorName is the human-readable vendor name
	VendorName string
	
	// DeviceType is the corresponding xw device type
	DeviceType api.DeviceType
}

// ChipModel represents a specific chip model with its PCI device ID
type ChipModel struct {
	// VendorID is the PCI vendor ID
	VendorID string
	
	// DeviceID is the PCI device ID
	DeviceID string
	
	// ModelName is the human-readable model name
	ModelName string
	
	// ConfigKey is the key used in runtime configuration (e.g., "ascend-910b")
	ConfigKey string
	
	// DeviceType is the corresponding xw device type
	DeviceType api.DeviceType
	
	// Generation is the chip generation (optional)
	Generation string
	
	// Capabilities lists the chip's capabilities
	Capabilities []string
}

// KnownVendors contains all supported chip vendors with their PCI IDs
//
// PCI Vendor IDs are standardized identifiers assigned by PCI-SIG.
// Each vendor has a unique 16-bit identifier.
//
// To get vendor IDs from your system:
//   lspci -nn | grep -i vendor_name
var KnownVendors = []ChipVendor{
	{
		VendorID:   "0x19e5",
		VendorName: "Huawei",
		DeviceType: api.DeviceTypeAscend,
	},
	// TODO: Add more vendors as needed
	// Example:
	// {
	//     VendorID:   "0x1db7",
	//     VendorName: "Baidu",
	//     DeviceType: api.DeviceTypeKunlun,
	// },
}

// KnownChips contains all supported chip models with their PCI IDs
//
// Each entry specifies:
//   - VendorID: PCI vendor identifier
//   - DeviceID: PCI device identifier for specific model
//   - ModelName: Human-readable model name
//   - DeviceType: Corresponding xw device type
//   - Generation: Chip generation/version
//   - Capabilities: List of supported features
//
// PCI IDs can be found using:
//   - lspci -nn (Linux)
//   - /sys/bus/pci/devices/*/vendor and device files
//   - Vendor documentation
//
// To add new chips:
//   1. Run: lspci -nn | grep "vendor_name"
//   2. Find the [vendor:device] IDs in square brackets
//   3. Add entry below with proper configuration
var KnownChips = []ChipModel{
	// Huawei Ascend Chips
	{
		VendorID:   "0x19e5",
		DeviceID:   "0xd802",
		ModelName:  "Ascend 910B",
		ConfigKey:  "ascend-910b",
		DeviceType: api.DeviceTypeAscend,
		Generation: "Ascend 9xx",
		Capabilities: []string{
			"int8", "int16", "fp16", "fp32",
			"inference", "training",
		},
	},
	{
		VendorID:   "0x19e5",
		DeviceID:   "0xd500",
		ModelName:  "Ascend 310P",
		ConfigKey:  "ascend-310p",
		DeviceType: api.DeviceTypeAscend,
		Generation: "Ascend 3xx",
		Capabilities: []string{
			"int8", "int16", "fp16", "fp32",
			"inference",
		},
	},
	// TODO: Add more chip models as they are verified
	// Example template:
	// {
	//     VendorID:   "0xXXXX",
	//     DeviceID:   "0xYYYY",
	//     ModelName:  "Chip Model Name",
	//     DeviceType: api.DeviceTypeXXX,
	//     Generation: "Generation Info",
	//     Capabilities: []string{
	//         "capability1", "capability2",
	//     },
	// },
}

// GetVendorByID returns the vendor information for a given PCI vendor ID
//
// Parameters:
//   - vendorID: PCI vendor ID (e.g., "0x1db7")
//
// Returns:
//   - Pointer to ChipVendor if found, nil otherwise
func GetVendorByID(vendorID string) *ChipVendor {
	for i := range KnownVendors {
		if KnownVendors[i].VendorID == vendorID {
			return &KnownVendors[i]
		}
	}
	return nil
}

// GetChipByID returns the chip model information for given PCI IDs
//
// Parameters:
//   - vendorID: PCI vendor ID
//   - deviceID: PCI device ID
//
// Returns:
//   - Pointer to ChipModel if found, nil otherwise
func GetChipByID(vendorID, deviceID string) *ChipModel {
	for i := range KnownChips {
		if KnownChips[i].VendorID == vendorID && 
		   KnownChips[i].DeviceID == deviceID {
			return &KnownChips[i]
		}
	}
	return nil
}

// GetChipsByVendor returns all chip models for a given vendor
//
// Parameters:
//   - vendorID: PCI vendor ID
//
// Returns:
//   - Slice of ChipModel for the vendor
func GetChipsByVendor(vendorID string) []ChipModel {
	var chips []ChipModel
	for _, chip := range KnownChips {
		if chip.VendorID == vendorID {
			chips = append(chips, chip)
		}
	}
	return chips
}

// GetChipsByDeviceType returns all chip models for a given device type
//
// Parameters:
//   - deviceType: The xw device type
//
// Returns:
//   - Slice of ChipModel for the device type
func GetChipsByDeviceType(deviceType api.DeviceType) []ChipModel {
	var chips []ChipModel
	for _, chip := range KnownChips {
		if chip.DeviceType == deviceType {
			chips = append(chips, chip)
		}
	}
	return chips
}

// IsKnownChip checks if a PCI ID combination is a known AI chip
//
// Parameters:
//   - vendorID: PCI vendor ID
//   - deviceID: PCI device ID
//
// Returns:
//   - true if the chip is known and supported
func IsKnownChip(vendorID, deviceID string) bool {
	return GetChipByID(vendorID, deviceID) != nil
}

// GetConfigKeyByModelName returns the configuration key for a chip model name.
//
// This function looks up the ConfigKey for a given ModelName from the KnownChips list.
// The ConfigKey is used in runtime configuration files (e.g., runtime_images.yaml).
//
// Parameters:
//   - modelName: Human-readable chip model name (e.g., "Ascend 910B", "Ascend 310P")
//
// Returns:
//   - Configuration key (e.g., "ascend-910b", "ascend-310p")
//   - Error if the model name is not found
//
// Example:
//   configKey, err := GetConfigKeyByModelName("Ascend 310P")
//   // Returns: "ascend-310p", nil
func GetConfigKeyByModelName(modelName string) (string, error) {
	for _, chip := range KnownChips {
		if chip.ModelName == modelName {
			if chip.ConfigKey == "" {
				return "", fmt.Errorf("chip model %s has no config key defined", modelName)
			}
			return chip.ConfigKey, nil
		}
	}
	return "", fmt.Errorf("unknown chip model: %s", modelName)
}

