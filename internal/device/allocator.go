package device

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// DeviceInfo represents information about a device for runtime use.
// This is a simplified version focused on runtime needs.
type DeviceInfo struct {
	Type       string            `json:"type"`         // Device type (e.g., "ascend", "cuda")
	Index      int               `json:"index"`        // Device index (0-based)
	BusAddress string            `json:"bus_address"`  // PCI bus address
	ModelName  string            `json:"model_name"`   // Device model name
	ConfigKey  string            `json:"config_key"`   // Configuration key for runtime_images.yaml
	Properties map[string]string `json:"properties"`   // Additional properties
}

// Allocator manages the allocation and release of physical devices.
// It scans for available AI accelerators and dynamically tracks which devices are
// allocated by querying running Docker containers.
type Allocator struct {
	mu           sync.RWMutex
	devices      []DeviceInfo      // All detected and available devices
	dockerClient *client.Client    // Docker client for querying container device usage
}

// NewAllocator creates and initializes a new DeviceAllocator.
//
// The allocator scans the system for AI accelerators and dynamically tracks
// device allocation by querying running Docker containers.
//
// Returns:
//   - Configured allocator
//   - Error if device scanning or Docker client creation fails
func NewAllocator() (*Allocator, error) {
	// Scan for AI chips
	chipsByType, err := FindAIChips()
	if err != nil {
		return nil, fmt.Errorf("failed to scan AI chips: %w", err)
	}

	// Convert detected chips to DeviceInfo and collect all devices
	var allDevices []DeviceInfo
	for deviceType, chips := range chipsByType {
		for idx, chip := range chips {
			deviceInfo := DeviceInfo{
				Type:       deviceType,
				Index:      idx,
				BusAddress: chip.BusAddress,
				ModelName:  chip.ModelName,
				ConfigKey:  chip.ConfigKey,
				Properties: map[string]string{
					"generation": chip.Generation,
					"vendor_id":  chip.VendorID,
					"device_id":  chip.DeviceID,
				},
			}
			allDevices = append(allDevices, deviceInfo)
		}
	}

	// Sort devices by type and index for consistent ordering
	sort.Slice(allDevices, func(i, j int) bool {
		if allDevices[i].Type != allDevices[j].Type {
			return allDevices[i].Type < allDevices[j].Type
		}
		return allDevices[i].Index < allDevices[j].Index
	})

	// Create Docker client for querying container device usage
	dockerClient, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	a := &Allocator{
		devices:      allDevices,
		dockerClient: dockerClient,
	}

	logger.Info("Device allocator initialized with %d devices (dynamic allocation from Docker)", len(allDevices))
	for i, dev := range allDevices {
		logger.Debug("Device %d: %s[%d] @ %s (%s)", 
			i, dev.Type, dev.Index, dev.BusAddress, dev.ModelName)
	}

	return a, nil
}

// Allocate attempts to allocate 'count' devices for a given instance.
//
// This method selects free devices by checking current Docker container allocations.
// The device allocation is tracked in the container labels, not in a separate state file.
//
// Parameters:
//   - instanceID: Unique identifier for the instance
//   - count: Number of devices to allocate
//
// Returns:
//   - Slice of allocated DeviceInfo
//   - Error if insufficient devices are available
func (a *Allocator) Allocate(instanceID string, count int) ([]DeviceInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Get currently allocated devices from Docker containers
	allocatedDevices, err := a.getAllocatedDevicesFromDocker()
	if err != nil {
		logger.Warn("Failed to query Docker for device allocations: %v", err)
		// Continue anyway, assuming no allocations
		allocatedDevices = make(map[int]bool)
	}

	// Find free devices
	var freeIndices []int
	for i := range a.devices {
		if !allocatedDevices[i] {
			freeIndices = append(freeIndices, i)
		}
	}

	// Check if enough free devices are available
	if len(freeIndices) < count {
		return nil, fmt.Errorf("insufficient free devices: requested %d, available %d", 
			count, len(freeIndices))
	}

	// Allocate the first 'count' free devices
	allocatedIndices := freeIndices[:count]

	// Prepare result
	result := make([]DeviceInfo, len(allocatedIndices))
	for i, idx := range allocatedIndices {
		result[i] = a.devices[idx]
	}

	logger.Info("Allocated %d device(s) to instance %s: %v (from %d free devices)", 
		count, instanceID, allocatedIndices, len(freeIndices))

	return result, nil
}

// Release frees devices previously allocated to an instance.
//
// Since devices are tracked via Docker containers, this method only logs
// the release. The actual device freeing happens when the container is stopped/removed.
//
// Parameters:
//   - instanceID: Unique identifier for the instance
//
// Returns:
//   - Always returns nil (kept for API compatibility)
func (a *Allocator) Release(instanceID string) error {
	// No-op: devices are automatically released when container is stopped/removed
	// The device allocation is tracked dynamically via Docker API
	logger.Debug("Release called for instance %s (devices auto-released via container lifecycle)", instanceID)
	return nil
}

// getAllocatedDevicesFromDocker queries Docker for containers with xw labels
// and extracts their device allocations.
//
// Returns:
//   - Map of device indices that are currently allocated
//   - Error if Docker query fails
func (a *Allocator) getAllocatedDevicesFromDocker() (map[int]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Query for all xw-managed containers (running or stopped)
	containers, err := a.dockerClient.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "xw.runtime"),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	allocated := make(map[int]bool)

	for _, c := range containers {
		// Only count running containers
		if c.State != "running" {
			continue
		}

		// Extract device indices from labels
		if deviceIndicesStr, ok := c.Labels["xw.device_indices"]; ok && deviceIndicesStr != "" {
			indices := parseDeviceIndices(deviceIndicesStr)
		for _, idx := range indices {
			allocated[idx] = true
		}
	}
	}

	return allocated, nil
}

// parseDeviceIndices parses a comma-separated string of device indices.
// Example: "0,1,2" -> []int{0, 1, 2}
func parseDeviceIndices(s string) []int {
	if s == "" {
	return nil
}

	parts := strings.Split(s, ",")
	indices := make([]int, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		var idx int
		if _, err := fmt.Sscanf(part, "%d", &idx); err == nil {
			indices = append(indices, idx)
		}
	}

	return indices
}

// GetAvailableDevices returns information about all available (free) devices.
//
// Returns:
//   - Slice of DeviceInfo for devices that are currently unallocated
func (a *Allocator) GetAvailableDevices() []DeviceInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Get currently allocated devices from Docker
	allocatedDevices, err := a.getAllocatedDevicesFromDocker()
	if err != nil {
		logger.Warn("Failed to query Docker for device allocations: %v", err)
		// Return all devices if we can't query Docker
		return a.devices
	}

	// Find free devices
	var available []DeviceInfo
	for i, dev := range a.devices {
		if !allocatedDevices[i] {
			available = append(available, dev)
		}
	}

	return available
}

// GetAllDevices returns information about all detected devices.
//
// Returns:
//   - Slice of all DeviceInfo (both allocated and free)
func (a *Allocator) GetAllDevices() []DeviceInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Return a copy to prevent external modification
	devices := make([]DeviceInfo, len(a.devices))
	copy(devices, a.devices)

	return devices
}

// GetAllocations returns a map of current device allocations from Docker containers.
//
// Returns:
//   - Map from instanceID to slice of allocated devices
func (a *Allocator) GetAllocations() map[string][]DeviceInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Query for all xw-managed running containers
	containers, err := a.dockerClient.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "xw.runtime"),
		),
	})
	if err != nil {
		logger.Warn("Failed to list containers: %v", err)
		return make(map[string][]DeviceInfo)
	}

	result := make(map[string][]DeviceInfo)

	for _, c := range containers {
		// Only count running containers
		if c.State != "running" {
			continue
		}

		instanceID := c.Labels["xw.instance_id"]
		if instanceID == "" {
			continue
		}

		// Extract device indices from labels
		if deviceIndicesStr, ok := c.Labels["xw.device_indices"]; ok && deviceIndicesStr != "" {
			indices := parseDeviceIndices(deviceIndicesStr)
			devices := make([]DeviceInfo, 0, len(indices))
			for _, idx := range indices {
				if idx >= 0 && idx < len(a.devices) {
					devices = append(devices, a.devices[idx])
		}
			}
			if len(devices) > 0 {
		result[instanceID] = devices
			}
		}
	}

	return result
}


