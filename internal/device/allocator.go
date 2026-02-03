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
	"github.com/tsingmaoai/xw-cli/internal/config"
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

// DeviceTopology provides distance information between logical chips.
//
// Topology enables distance-aware device allocation to minimize inter-chip
// communication latency by preferring chips with shorter distances.
type DeviceTopology struct {
	// chipToBox maps logical chip index to box index
	// Example: {0: 0, 1: 0, 2: 0, 3: 0, 4: 1, 5: 1, 6: 1, 7: 1}
	chipToBox map[int]int
	
	// boxes stores the box configuration from config
	boxes []config.TopologyBox
}

// NewDeviceTopology creates a topology from configuration.
//
// Parameters:
//   - topologyConfig: Topology configuration from devices.yaml
//
// Returns:
//   - Initialized DeviceTopology or nil if no topology configured
func NewDeviceTopology(topologyConfig *config.TopologyConfig) *DeviceTopology {
	if topologyConfig == nil || len(topologyConfig.Boxes) == 0 {
		return nil
	}
	
	dt := &DeviceTopology{
		chipToBox: make(map[int]int),
		boxes:     topologyConfig.Boxes,
	}
	
	// Build chip-to-box mapping (using logical chip indices directly)
	for boxIdx, box := range topologyConfig.Boxes {
		for _, chipIdx := range box.Devices {
			dt.chipToBox[chipIdx] = boxIdx
		}
	}
	
	return dt
}

// GetDistance calculates the distance between two logical chips.
//
// Distance rules:
//   - Same box: distance = 0 (high-speed interconnect)
//   - Different boxes: distance = |box_a - box_b|
//   - Unknown chip: distance = 999 (avoid allocation)
//
// Parameters:
//   - chipA: Logical chip index A
//   - chipB: Logical chip index B
//
// Returns:
//   - Distance value (0 = closest, higher = farther)
func (dt *DeviceTopology) GetDistance(chipA, chipB int) int {
	if dt == nil {
		// No topology configured, all chips have equal distance
		return 0
	}
	
	boxA, okA := dt.chipToBox[chipA]
	boxB, okB := dt.chipToBox[chipB]
	
	// If either chip is not in topology, assign high distance
	if !okA || !okB {
		return 999
	}
	
	// Same box = zero distance
	if boxA == boxB {
		return 0
	}
	
	// Different boxes = box index difference
	diff := boxA - boxB
	if diff < 0 {
		diff = -diff
	}
	return diff
}

// Allocator manages the allocation and release of physical devices.
// It scans for available AI accelerators and dynamically tracks which devices are
// allocated by querying running Docker containers.
//
// The allocator supports topology-aware allocation to optimize device placement
// for high-speed interconnected devices (e.g., NVLink, HCCS).
type Allocator struct {
	mu               sync.RWMutex
	devices          []DeviceInfo                   // All detected and available devices
	dockerClient     *client.Client                 // Docker client for querying container device usage
	topologyByType   map[string]*DeviceTopology     // Topology per device type (e.g., "ascend-910b" -> topology)
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
					"generation":            chip.Generation,
					"vendor_id":             chip.VendorID,
					"device_id":             chip.DeviceID,
					"physical_device_index": fmt.Sprintf("%d", chip.PhysicalDeviceIndex),
					"chip_index":            fmt.Sprintf("%d", chip.ChipIndex),
					"chips_per_device":      fmt.Sprintf("%d", chip.ChipsPerDevice),
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

	// Load topology configuration per device type for distance-aware allocation
	devConfig, err := config.LoadDevicesConfig()
	topologyByType := make(map[string]*DeviceTopology)
	if err == nil {
		// Load topology for each chip model
		for _, vendor := range devConfig.Vendors {
			for _, chipModel := range vendor.ChipModels {
				if chipModel.Topology != nil && len(chipModel.Topology.Boxes) > 0 {
					topology := NewDeviceTopology(chipModel.Topology)
					if topology != nil {
						topologyByType[chipModel.ConfigKey] = topology
						logger.Info("Loaded topology for %s: %d boxes", chipModel.ConfigKey, len(chipModel.Topology.Boxes))
					}
				}
			}
		}
	}

	a := &Allocator{
		devices:        allDevices,
		dockerClient:   dockerClient,
		topologyByType: topologyByType,
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

	// Group free devices by ConfigKey (chip model)
	// This ensures we allocate devices of the same model, each with their own topology
	freeByConfigKey := make(map[string][]int)
	for i := range a.devices {
		if !allocatedDevices[i] {
			configKey := a.devices[i].ConfigKey
			freeByConfigKey[configKey] = append(freeByConfigKey[configKey], i)
		}
	}

	// Find a chip model with enough free devices
	var selectedConfigKey string
	var freeIndices []int
	for configKey, indices := range freeByConfigKey {
		if len(indices) >= count {
			selectedConfigKey = configKey
			freeIndices = indices
			break
		}
	}

	// Check if enough free devices are available
	if len(freeIndices) < count {
		// Calculate total free devices across all models for error message
		totalFree := 0
		for _, indices := range freeByConfigKey {
			totalFree += len(indices)
		}
		return nil, fmt.Errorf("insufficient free devices of same model: requested %d, available %d total (spread across different models)", 
			count, totalFree)
	}

	// Select best devices using topology-aware allocation (within same chip model)
	allocatedIndices := a.selectBestDevices(freeIndices, count, selectedConfigKey)

	// Prepare result
	result := make([]DeviceInfo, len(allocatedIndices))
	for i, idx := range allocatedIndices {
		result[i] = a.devices[idx]
	}

	logger.Info("Allocated %d %s device(s) to instance %s: indices %v (from %d free of this model)", 
		count, selectedConfigKey, instanceID, allocatedIndices, len(freeIndices))

	return result, nil
}

// selectBestDevices selects the optimal chip combination using topology information.
//
// This method implements topology-aware chip selection to minimize total distance
// between allocated chips. The algorithm:
//   1. If no topology for chip model: select first N chips (backward compatible)
//   2. With topology: find chip combination with minimum total distance
//
// For N chips, total distance = sum of all pairwise distances.
// This ensures allocated chips are as close as possible in physical topology.
//
// Parameters:
//   - freeIndices: Logical chip indices of available chips
//   - count: Number of chips to select
//   - configKey: Chip model config key (e.g., "ascend-910b", "ascend-310p") to find corresponding topology
//
// Returns:
//   - Selected logical chip indices optimized for topology
func (a *Allocator) selectBestDevices(freeIndices []int, count int, configKey string) []int {
	// Get topology for this chip model
	topology := a.topologyByType[configKey]
	
	// No topology or single chip: use simple selection
	if topology == nil || count == 1 {
		return freeIndices[:count]
	}
	
	// Try to find chips all in same box (distance = 0)
	bestIndices := freeIndices[:count]
	bestDistance := a.calculateTotalDistance(bestIndices, topology)
	
	// If best distance is already 0, we found optimal allocation (all in same box)
	if bestDistance == 0 {
		logger.Debug("Topology-aware allocation for %s: found %d chips in same box (distance=0)", configKey, count)
		return bestIndices
	}
	
	// Try different combinations to find better allocation
	// For small counts, try a few different starting positions
	maxAttempts := 10
	if len(freeIndices) < maxAttempts {
		maxAttempts = len(freeIndices)
	}
	
	for start := 1; start < maxAttempts && start+count <= len(freeIndices); start++ {
		candidate := freeIndices[start : start+count]
		distance := a.calculateTotalDistance(candidate, topology)
		
		if distance < bestDistance {
			bestDistance = distance
			bestIndices = candidate
			
			// Found optimal allocation (same box)
			if bestDistance == 0 {
				break
			}
		}
	}
	
	logger.Debug("Topology-aware allocation for %s: selected %d chips with total distance=%d", configKey, count, bestDistance)
	return bestIndices
}

// calculateTotalDistance calculates the sum of pairwise distances for a chip set.
//
// Parameters:
//   - chipIndices: Logical chip indices to evaluate
//   - topology: Topology configuration to use for distance calculation
//
// Returns:
//   - Total distance (sum of all pairwise distances)
func (a *Allocator) calculateTotalDistance(chipIndices []int, topology *DeviceTopology) int {
	if len(chipIndices) <= 1 {
		return 0
	}
	
	totalDistance := 0
	for i := 0; i < len(chipIndices); i++ {
		for j := i + 1; j < len(chipIndices); j++ {
			// Use logical chip indices directly (no conversion needed)
			totalDistance += topology.GetDistance(chipIndices[i], chipIndices[j])
		}
	}
	
	return totalDistance
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


