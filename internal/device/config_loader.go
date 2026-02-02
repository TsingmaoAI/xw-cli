// Package device - config_loader.go provides configuration-based device loading.
//
// This module loads device and chip information from configuration files,
// making them available to the device detection and management system.
package device

import (
	"github.com/tsingmaoai/xw-cli/internal/api"
	"github.com/tsingmaoai/xw-cli/internal/config"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// LoadVendorsFromConfig loads vendor information from device configuration.
//
// This function reads the device configuration file and extracts vendor
// information, making it available for device identification and display.
//
// Returns:
//   - Slice of ChipVendor structs
//   - Empty slice if configuration loading fails
//
// Example:
//
//	vendors := LoadVendorsFromConfig()
//	for _, vendor := range vendors {
//	    fmt.Printf("Vendor: %s (%s)\n", vendor.VendorName, vendor.VendorID)
//	}
func LoadVendorsFromConfig() []ChipVendor {
	devConfig, err := config.LoadDevicesConfig()
	if err != nil {
		return []ChipVendor{}
	}
	
	var vendors []ChipVendor
	for _, vendor := range devConfig.Vendors {
		vendors = append(vendors, ChipVendor{
			VendorID:   vendor.VendorID,
			VendorName: vendor.VendorName,
		})
	}
	
	logger.Debug("Loaded %d vendor(s) from configuration", len(vendors))
	return vendors
}

// LoadChipsFromConfig loads chip model information from device configuration.
//
// This function reads the device configuration file and extracts chip model
// details, including PCI IDs, capabilities, and device types.
//
// Returns:
//   - Slice of ChipModel structs
//   - Empty slice if configuration loading fails
//
// Example:
//
//	chips := LoadChipsFromConfig()
//	for _, chip := range chips {
//	    fmt.Printf("Chip: %s (%s:%s)\n", chip.ModelName, chip.VendorID, chip.DeviceID)
//	}
func LoadChipsFromConfig() []ChipModel {
	chipModels, err := config.LoadChipModels()
	if err != nil {
		return []ChipModel{}
	}
	
	var chips []ChipModel
	for _, chip := range chipModels {
		chips = append(chips, ChipModel{
			VendorID:     chip.VendorID,
			DeviceID:     chip.DeviceID,
			ModelName:    chip.ModelName,
			ConfigKey:    chip.ConfigKey,
			DeviceType:   chip.DeviceType,
			Generation:   chip.Generation,
			Capabilities: chip.Capabilities,
		})
	}
	
	logger.Debug("Loaded %d chip model(s) from configuration", len(chips))
	return chips
}

// GetChipByID looks up a chip model by PCI vendor and device IDs.
//
// This function searches the configured chip models for a match with the
// specified PCI identifiers. It's commonly used during device detection
// to identify discovered hardware.
//
// Parameters:
//   - vendorID: PCIe vendor ID (e.g., "0x19e5")
//   - deviceID: PCIe device ID (e.g., "0xd802")
//
// Returns:
//   - Pointer to ChipModel if found
//   - nil if not found
//
// Example:
//
//	chip := GetChipByID("0x19e5", "0xd802")
//	if chip != nil {
//	    fmt.Printf("Found: %s\n", chip.ModelName)
//	}
func GetChipByID(vendorID, deviceID string) *ChipModel {
	chips := LoadChipsFromConfig()
	for i := range chips {
		if chips[i].VendorID == vendorID && chips[i].DeviceID == deviceID {
			return &chips[i]
		}
	}
	return nil
}

// GetChipsByVendor returns all chip models for a specific vendor.
//
// This function filters chip models by vendor ID, returning only those
// that match. Useful for displaying vendor-specific chip information.
//
// Parameters:
//   - vendorID: PCIe vendor ID to filter by
//
// Returns:
//   - Slice of ChipModel structs matching the vendor
//
// Example:
//
//	chips := GetChipsByVendor("0x19e5")
//	fmt.Printf("Huawei has %d chip model(s)\n", len(chips))
func GetChipsByVendor(vendorID string) []ChipModel {
	chips := LoadChipsFromConfig()
	var vendorChips []ChipModel
	for _, chip := range chips {
		if chip.VendorID == vendorID {
			vendorChips = append(vendorChips, chip)
		}
	}
	return vendorChips
}

// GetChipsByDeviceType returns all chip models for a specific device type.
//
// This function filters chip models by device type, useful for showing
// all chips that support a particular device type.
//
// Parameters:
//   - deviceType: The device type to filter by
//
// Returns:
//   - Slice of ChipModel structs matching the device type
//
// Example:
//
//	chips := GetChipsByDeviceType(api.DeviceType("ascend-910b"))
//	for _, chip := range chips {
//	    fmt.Printf("- %s\n", chip.ModelName)
//	}
func GetChipsByDeviceType(deviceType api.DeviceType) []ChipModel {
	chips := LoadChipsFromConfig()
	var typeChips []ChipModel
	for _, chip := range chips {
		if chip.DeviceType == deviceType {
			typeChips = append(typeChips, chip)
		}
	}
	return typeChips
}

