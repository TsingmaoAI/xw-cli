// Package device provides domestic chip device detection and management.
//
// This package handles detection and management of Chinese-made chip devices
// including:
//   - Hardware detection and capability querying
//   - Device availability checking
//   - Device metadata and properties
//   - Thread-safe access to device information
//
// The package currently supports Huawei Ascend NPU chips (910B, 310P).
// In production deployments, this package integrates with vendor-specific
// drivers and libraries for actual hardware detection.
package device

import (
	"fmt"
	"strings"
	"sync"

	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/config"
	"github.com/tsingmao/xw/internal/logger"
)

// Device represents a detected domestic chip device with its metadata.
//
// A Device instance contains information about a specific hardware device
// including its type, availability status, and vendor-specific properties.
// This information is used to determine model compatibility and optimize
// model execution.
type Device struct {
	// Type is the device type identifying the chip architecture.
	// Example: DeviceTypeAscend
	Type api.DeviceType `json:"type"`

	// Name is the human-readable device name.
	// Example: "Huawei Ascend 910B"
	Name string `json:"name"`

	// Available indicates if the device is currently available for use.
	// A device may be unavailable if it's in use, has an error, or lacks drivers.
	Available bool `json:"available"`

	// Properties contains device-specific metadata and capabilities.
	// Common keys: "vendor", "version", "memory", "cores"
	// Values are stored as strings for flexibility.
	Properties map[string]string `json:"properties"`
}

// Manager manages device detection and maintains device availability state.
//
// The Manager provides thread-safe access to information about detected
// hardware devices. It performs initial device detection at creation and
// maintains a registry of available devices.
//
// In production, the Manager would integrate with vendor-specific APIs and
// drivers to perform actual hardware probing and capability detection.
type Manager struct {
	// mu provides thread-safe access to the devices map.
	// Uses RWMutex to allow multiple concurrent readers.
	mu sync.RWMutex

	// devices maps device types to their detected instances.
	// Key: device type (e.g., ConfigKeyAscend910B)
	// Value: pointer to Device with detection results
	devices map[api.DeviceType]*Device
}

// NewManager creates and initializes a new device manager.
//
// The manager is created with an empty devices map and immediately performs
// device detection through detectDevices(). This identifies all available
// domestic chip devices on the system.
//
// Device detection happens synchronously during initialization to ensure
// device information is available immediately after creation.
//
// Returns:
//   - A pointer to a fully initialized Manager with detected devices.
//
// Example:
//
//	manager := device.NewManager()
//	if manager.IsAvailable(ConfigKeyAscend910B) {
//	    fmt.Println("Ascend 910B NPU is available")
//	}
func NewManager() *Manager {
	m := &Manager{
		devices: make(map[api.DeviceType]*Device),
	}
	m.detectDevices()
	return m
}

// detectDevices performs hardware detection to identify available devices.
//
// This method probes the system for supported domestic chip devices using
// PCI device scanning. For each detected device type, it creates a Device
// entry with metadata including chip count and capabilities.
//
// The detection uses the FindAIChips() function which:
//   - Scans PCI devices via sysfs (/sys/bus/pci/devices)
//   - Matches against known AI chip vendor/device IDs
//   - Returns detected chips grouped by device type
//
// Note: This provides basic detection via PCI IDs. For advanced features like
// driver version checking, memory info, etc., vendor-specific SDKs would be needed.
func (m *Manager) detectDevices() {
	// Scan for actual AI chips via PCI
	chips, err := FindAIChips()
	if err != nil {
		// If scanning fails, log but don't crash
		// The devices map will remain empty (no devices detected)
		return
	}

	// Convert detected chips to Device entries
	// Group all chips of the same type into a single Device entry
	for _, chipList := range chips {
		if len(chipList) == 0 {
			continue
		}

		// Get the first chip for metadata (all chips of same type share properties)
		firstChip := chipList[0]
		deviceType := firstChip.DeviceType

		device := &Device{
			Type:      deviceType,
			Name:      firstChip.ModelName,
			Available: true,
			Properties: map[string]string{
				"count":      fmt.Sprintf("%d", len(chipList)),
				"generation": firstChip.Generation,
			},
		}

		// Add capabilities if available
		if len(firstChip.Capabilities) > 0 {
			device.Properties["capabilities"] = strings.Join(firstChip.Capabilities, ",")
		}

		m.devices[deviceType] = device
	}
}

// ListAvailable returns all currently available devices.
//
// This method returns only devices that are marked as available, filtering
// out devices that are unavailable due to errors, being in use, or missing
// drivers.
//
// The method is thread-safe and can be called concurrently. It returns
// pointers to Device structs, allowing callers to inspect detailed device
// properties.
//
// Returns:
//   - A slice of pointers to Device structs for all available devices.
//     Returns an empty slice if no devices are available.
//
// Example:
//
//	devices := manager.ListAvailable()
//	for _, dev := range devices {
//	    fmt.Printf("%s (%s): vendor=%s\n",
//	        dev.Name, dev.Type, dev.Properties["vendor"])
//	}
func (m *Manager) ListAvailable() []*Device {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Device
	for _, device := range m.devices {
		if device.Available {
			result = append(result, device)
		}
	}

	return result
}

// IsAvailable checks if a specific device type is currently available.
//
// This method performs a quick check to determine if a device of the specified
// type exists and is marked as available. It's commonly used before attempting
// to run models on specific hardware.
//
// The method is thread-safe and can be called concurrently.
//
// Parameters:
//   - deviceType: The device type to check (e.g., ConfigKeyAscend910B)
//
// Returns:
//   - true if the device type exists and is available
//   - false if the device doesn't exist or is unavailable
//
// Example:
//
//	if !manager.IsAvailable(ConfigKeyAscend910B) {
//	    return fmt.Errorf("Ascend 910B device required but not available")
//	}
func (m *Manager) IsAvailable(deviceType api.DeviceType) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	device, exists := m.devices[deviceType]
	if !exists {
		return false
	}

	return device.Available
}

// GetDevice retrieves detailed information for a specific device type.
//
// This method returns the full Device struct for a specified device type,
// including all properties and metadata. Unlike IsAvailable(), this method
// returns detailed information even if the device is unavailable, allowing
// callers to determine why a device isn't available.
//
// The method is thread-safe and can be called concurrently.
//
// Parameters:
//   - deviceType: The device type to retrieve information for
//
// Returns:
//   - A pointer to the Device struct if the device type is detected
//   - An error if the device type was not detected on the system
//
// Example:
//
//	device, err := manager.GetDevice(ConfigKeyAscend910B)
//	if err != nil {
//	    log.Printf("Ascend device not found: %v", err)
//	    return
//	}
//	fmt.Printf("Device version: %s\n", device.Properties["version"])
func (m *Manager) GetDevice(deviceType api.DeviceType) (*Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	device, exists := m.devices[deviceType]
	if !exists {
		return nil, fmt.Errorf("device type %s not found", deviceType)
	}

	return device, nil
}

// GetSupportedTypes returns all device types supported by the application.
//
// This method returns a complete list of all device types that the xw
// application is designed to work with, regardless of whether they are
// currently detected on the system. This is useful for:
//   - Displaying supported hardware to users
//   - Validation of configuration files
//   - Documentation and help text generation
//
// The method reads device types from configuration. If configuration loading
// fails, it returns a fallback list of known device types.
//
// Returns:
//   - A slice of all supported DeviceType values.
//
// Example:
//
//	supported := manager.GetSupportedTypes()
//	fmt.Printf("This application supports %d device types\n", len(supported))
//	for _, dt := range supported {
//	    fmt.Printf("- %s\n", dt)
//	}
func (m *Manager) GetSupportedTypes() []api.DeviceType {
	// Load from configuration
	types, err := config.GetSupportedDeviceTypes()
	if err != nil {
		// Configuration is required
		logger.Warn("Failed to load device types from configuration: %v", err)
		return []api.DeviceType{}
	}
	return types
}

// GetDetectedDeviceTypes returns the types of all detected devices.
//
// This method returns only the device types that have been detected on
// the current system. It's used to filter models to show only those
// compatible with available hardware.
//
// Returns:
//   - A slice of DeviceType values for detected devices.
//     Returns an empty slice if no devices are detected.
//
// Example:
//
//	detected := manager.GetDetectedDeviceTypes()
//	if len(detected) == 0 {
//	    fmt.Println("No AI accelerators detected")
//	} else {
//	    fmt.Printf("Detected: %v\n", detected)
//	}
func (m *Manager) GetDetectedDeviceTypes() []api.DeviceType {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var types []api.DeviceType
	for deviceType, device := range m.devices {
		if device.Available {
			types = append(types, deviceType)
		}
	}

	return types
}

// ListDetectedChips returns detailed information for all detected AI chips.
//
// This method performs a fresh scan of the system and returns individual
// chip information including PCI addresses, vendor/device IDs, and capabilities.
// Unlike ListAvailable() which returns aggregated Device entries, this returns
// one entry per physical chip.
//
// Returns:
//   - A slice of DetectedChip with details for each physical chip
//   - An error if hardware scanning fails
//
// Example:
//
//	chips, err := manager.ListDetectedChips()
//	if err != nil {
//	    return err
//	}
//	for _, chip := range chips {
//	    fmt.Printf("%s: %s at %s\n", chip.DeviceType, chip.ModelName, chip.BusAddress)
//	}
func (m *Manager) ListDetectedChips() ([]DetectedChip, error) {
	// Scan for AI chips
	chipsMap, err := FindAIChips()
	if err != nil {
		return nil, fmt.Errorf("failed to find AI chips: %w", err)
	}

	// Flatten the map into a single slice
	var allChips []DetectedChip
	for _, chips := range chipsMap {
		allChips = append(allChips, chips...)
	}

	return allChips, nil
}
