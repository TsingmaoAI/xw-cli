// Package device provides domestic chip device detection and management.
package device

import (
	"github.com/tsingmaoai/xw-cli/internal/api"
)

// ChipVendor represents a chip vendor's PCI vendor ID and name
type ChipVendor struct {
	// VendorID is the PCI vendor ID (e.g., "0x19e5" for Huawei)
	VendorID string
	
	// VendorName is the human-readable vendor name
	VendorName string
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

// KnownVendors loads and caches vendors from configuration
var KnownVendors = LoadVendorsFromConfig()

// KnownChips loads and caches chip models from configuration
var KnownChips = LoadChipsFromConfig()
