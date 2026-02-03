package device

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	
	"github.com/tsingmaoai/xw-cli/internal/api"
	"github.com/tsingmaoai/xw-cli/internal/config"
)

// PCIDevice represents a PCI device with its identifiers
type PCIDevice struct {
	// VendorID is the PCI vendor ID (e.g., "0x1db7")
	VendorID string
	
	// DeviceID is the PCI device ID
	DeviceID string
	
	// SubsystemVendorID is the subsystem vendor ID (optional)
	SubsystemVendorID string
	
	// SubsystemDeviceID is the subsystem device ID (optional)
	SubsystemDeviceID string
	
	// BusAddress is the PCI bus address (e.g., "0000:01:00.0")
	BusAddress string
	
	// Class is the PCI device class
	Class string
}

// ScanPCIDevices scans the system for PCI devices
//
// This function reads PCI device information from /sys/bus/pci/devices
// which is the standard location on Linux systems.
//
// Returns:
//   - Slice of PCIDevice found on the system
//   - Error if scanning fails
func ScanPCIDevices() ([]PCIDevice, error) {
	const pciDevicesPath = "/sys/bus/pci/devices"
	
	// Check if PCI sysfs path exists
	if _, err := os.Stat(pciDevicesPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("PCI devices path not found: %s", pciDevicesPath)
	}
	
	// Read all PCI device directories
	entries, err := os.ReadDir(pciDevicesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read PCI devices: %w", err)
	}
	
	var devices []PCIDevice
	
	for _, entry := range entries {
		// PCI device entries are symlinks, not directories
		devicePath := filepath.Join(pciDevicesPath, entry.Name())
		device, err := readPCIDevice(devicePath, entry.Name())
		if err != nil {
			// Log but don't fail on individual device read errors
			continue
		}
		
		devices = append(devices, device)
	}
	
	return devices, nil
}

// readPCIDevice reads PCI device information from sysfs
func readPCIDevice(devicePath, busAddress string) (PCIDevice, error) {
	device := PCIDevice{
		BusAddress: busAddress,
	}
	
	// Read vendor ID
	vendorID, err := readPCIFile(filepath.Join(devicePath, "vendor"))
	if err != nil {
		return device, err
	}
	device.VendorID = strings.TrimSpace(vendorID)
	
	// Read device ID
	deviceID, err := readPCIFile(filepath.Join(devicePath, "device"))
	if err != nil {
		return device, err
	}
	device.DeviceID = strings.TrimSpace(deviceID)
	
	// Read subsystem vendor ID (optional)
	if subsysVendor, err := readPCIFile(filepath.Join(devicePath, "subsystem_vendor")); err == nil {
		device.SubsystemVendorID = strings.TrimSpace(subsysVendor)
	}
	
	// Read subsystem device ID (optional)
	if subsysDevice, err := readPCIFile(filepath.Join(devicePath, "subsystem_device")); err == nil {
		device.SubsystemDeviceID = strings.TrimSpace(subsysDevice)
	}
	
	// Read device class (optional)
	if class, err := readPCIFile(filepath.Join(devicePath, "class")); err == nil {
		device.Class = strings.TrimSpace(class)
	}
	
	return device, nil
}

// readPCIFile reads a single line from a PCI sysfs file
func readPCIFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// FindAIChips scans for known AI chips on the system
//
// This function combines PCI device scanning with the chip configuration
// to identify AI accelerators present in the system.
//
// For multi-chip cards (where chips_per_device > 1 in config), each physical
// PCI device is expanded into multiple logical chips with consecutive indices.
// For example, if 4 dual-chip cards are detected, this returns 8 logical chips
// with indices 0-7, allowing the allocator to treat them as independent devices.
//
// Returns:
//   - Map of device type to slice of detected chips (logical chips with consecutive indices)
//   - Error if scanning fails
func FindAIChips() (map[string][]DetectedChip, error) {
	devices, err := ScanPCIDevices()
	if err != nil {
		return nil, fmt.Errorf("failed to scan PCI devices: %w", err)
	}
	
	// Load device configuration once before the loop
	devConfig, err := config.LoadDevicesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load device configuration: %w", err)
	}
	
	detected := make(map[string][]DetectedChip)
	physicalDeviceCount := make(map[string]int) // Track physical device count per device type
	
	for _, device := range devices {
		// Lookup chip from configuration using pre-loaded config
		vendor, model := config.FindChipModelByIdentifier(devConfig, device.VendorID, device.DeviceID)
		if vendor == nil || model == nil {
			// Not a known AI chip in configuration
			continue
		}
		
		deviceType := string(model.ConfigKey)
		
		// Get current physical device index for this device type
		physicalIdx := physicalDeviceCount[deviceType]
		physicalDeviceCount[deviceType]++
		
		// Get chips_per_device from config (default to 1 for single-chip cards)
		chipsPerDevice := model.ChipsPerDevice
		if chipsPerDevice <= 0 {
			chipsPerDevice = 1
		}
		
		// Expand multi-chip cards: create one DetectedChip per chip
		// Each gets a consecutive index based on current count for this device type
		for chipIdx := 0; chipIdx < chipsPerDevice; chipIdx++ {
			detectedChip := DetectedChip{
				VendorID:            device.VendorID,
				DeviceID:            device.DeviceID,
				BusAddress:          device.BusAddress,
				ModelName:           model.ModelName,
				ConfigKey:           model.ConfigKey,
				DeviceType:          api.DeviceType(model.ConfigKey),
				Generation:          model.Generation,
				Capabilities:        model.Capabilities,
				PhysicalDeviceIndex: physicalIdx,
				ChipIndex:           chipIdx,
				ChipsPerDevice:      chipsPerDevice,
			}
			
		detected[deviceType] = append(detected[deviceType], detectedChip)
		}
	}
	
	return detected, nil
}

// DetectedChip represents a detected AI chip with full information
type DetectedChip struct {
	// VendorID is the PCI vendor ID
	VendorID string `json:"vendor_id"`
	
	// DeviceID is the PCI device ID
	DeviceID string `json:"device_id"`
	
	// BusAddress is the PCI bus address
	BusAddress string `json:"bus_address"`
	
	// ModelName is the chip model name
	ModelName string `json:"model_name"`
	
	// ConfigKey is the key used in runtime configuration
	ConfigKey string `json:"config_key"`
	
	// DeviceType is the xw device type
	DeviceType api.DeviceType `json:"device_type"`
	
	// Generation is the chip generation
	Generation string `json:"generation"`
	
	// Capabilities lists the chip's capabilities
	Capabilities []string `json:"capabilities"`
	
	// PhysicalDeviceIndex is the index of the physical PCI device (0-based)
	// Used to identify which physical card this chip belongs to
	PhysicalDeviceIndex int `json:"physical_device_index"`
	
	// ChipIndex is the chip index within a multi-chip card (0-based)
	// For single-chip cards: 0
	// For dual-chip cards: 0 or 1
	ChipIndex int `json:"chip_index"`
	
	// ChipsPerDevice indicates total chips on this physical device
	ChipsPerDevice int `json:"chips_per_device"`
}

// ParseLspciOutput parses the output of `lspci -nn` command
//
// This is an alternative method for systems where sysfs access is restricted.
// The output format should be: "bus:dev.fn Class [class]: Vendor [vid:did]"
//
// Parameters:
//   - output: The output from lspci -nn command
//
// Returns:
//   - Slice of PCIDevice parsed from the output
func ParseLspciOutput(output string) []PCIDevice {
	var devices []PCIDevice
	
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		device := parseLspciLine(line)
		if device != nil {
			devices = append(devices, *device)
		}
	}
	
	return devices
}

// parseLspciLine parses a single line from lspci -nn output
func parseLspciLine(line string) *PCIDevice {
	// Example: "01:00.0 Processing accelerators [1200]: Baidu [1db7:0001]"
	
	// Extract bus address
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil
	}
	
	device := &PCIDevice{
		BusAddress: fields[0],
	}
	
	// Extract vendor:device IDs from [vid:did]
	idStart := strings.Index(line, "[")
	idEnd := strings.LastIndex(line, "]")
	if idStart == -1 || idEnd == -1 || idEnd <= idStart {
		return nil
	}
	
	// Find the last [vid:did] pattern (device IDs)
	lastBracket := strings.LastIndex(line, "[")
	if lastBracket != -1 && strings.Contains(line[lastBracket:], ":") {
		ids := line[lastBracket+1 : idEnd]
		parts := strings.Split(ids, ":")
		if len(parts) == 2 {
			device.VendorID = "0x" + strings.TrimSpace(parts[0])
			device.DeviceID = "0x" + strings.TrimSpace(parts[1])
		}
	}
	
	return device
}

