package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/device"
	"github.com/tsingmao/xw/internal/logger"
)

// Manager manages multiple runtime implementations.
type Manager struct {
	mu              sync.RWMutex
	runtimes        map[string]Runtime
	deviceAllocator *device.Allocator // Lazy-initialized device allocator
	stopCh          chan struct{}
	wg              sync.WaitGroup
}

// NewManager creates a new runtime manager.
func NewManager() (*Manager, error) {
	return &Manager{
		runtimes:        make(map[string]Runtime),
		deviceAllocator: nil, // Lazy-initialized on first use
		stopCh:          make(chan struct{}),
	}, nil
	}
	
// getOrCreateAllocator gets the device allocator, creating it if necessary.
// This is called internally when devices need to be allocated.
// The allocator now queries Docker directly for device allocations instead of using a state file.
func (m *Manager) getOrCreateAllocator(configDir string) (*device.Allocator, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.deviceAllocator == nil {
		allocator, err := device.NewAllocator()
		if err != nil {
			return nil, fmt.Errorf("failed to create device allocator: %w", err)
		}
		m.deviceAllocator = allocator
	}
	
	return m.deviceAllocator, nil
}

// RegisterRuntime registers a runtime implementation.
func (m *Manager) RegisterRuntime(runtime Runtime) error {
	if runtime == nil {
		return fmt.Errorf("runtime cannot be nil")
	}
	
	name := runtime.Name()
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if _, exists := m.runtimes[name]; exists {
		return fmt.Errorf("runtime %s already registered", name)
		}
	
	m.runtimes[name] = runtime
	return nil
	}
	
// Create creates an instance using the specified runtime.
func (m *Manager) Create(ctx context.Context, runtimeName string, params *CreateParams) (*Instance, error) {
	m.mu.RLock()
	rt, exists := m.runtimes[runtimeName]
	m.mu.RUnlock()
	
	if !exists {
		return nil, fmt.Errorf("runtime %s not found", runtimeName)
	}
	
	return rt.Create(ctx, params)
	}
	
// Start starts an instance.
func (m *Manager) Start(ctx context.Context, instanceID string) error {
	rt, _, err := m.findInstanceRuntime(ctx, instanceID)
	if err != nil {
		return err
	}
	return rt.Start(ctx, instanceID)
}

// Stop stops an instance and releases its allocated devices.
//
// This method stops the instance and removes its container.
// Allocated devices are released back to the pool.
func (m *Manager) Stop(ctx context.Context, instanceID string) error {
	rt, _, err := m.findInstanceRuntime(ctx, instanceID)
	if err != nil {
		return err
	}
	
	// Stop the instance (which now also removes the container)
	if err := rt.Stop(ctx, instanceID); err != nil {
		return err
	}
	
	// Release allocated devices if allocator is initialized
	if m.deviceAllocator != nil {
		if err := m.deviceAllocator.Release(instanceID); err != nil {
			logger.Warn("Failed to release devices for instance %s: %v", instanceID, err)
		}
	}
	
	return nil
}
	
// Remove removes an instance and releases its allocated devices.
func (m *Manager) Remove(ctx context.Context, instanceID string) error {
	rt, _, err := m.findInstanceRuntime(ctx, instanceID)
	if err != nil {
		return err
	}
	
	// Remove the instance from runtime
	if err := rt.Remove(ctx, instanceID); err != nil {
		return err
	}
	
	// Release allocated devices if allocator is initialized
	if m.deviceAllocator != nil {
		if err := m.deviceAllocator.Release(instanceID); err != nil {
			logger.Warn("Failed to release devices for instance %s: %v", instanceID, err)
		}
	}
	
	return nil
}

// Get retrieves a specific instance by ID across all runtimes.
//
// This method searches all registered runtimes to find the instance
// with the specified ID. It returns the first matching instance found.
//
// Returns:
//   - The instance if found
//   - Error if instance not found or lookup fails
func (m *Manager) Get(ctx context.Context, instanceID string) (*Instance, error) {
	_, instance, err := m.findInstanceRuntime(ctx, instanceID)
	return instance, err
}

// List lists all instances across all runtimes.
func (m *Manager) List(ctx context.Context) ([]*Instance, error) {
	m.mu.RLock()
	runtimes := make([]Runtime, 0, len(m.runtimes))
	for _, rt := range m.runtimes {
		runtimes = append(runtimes, rt)
	}
	m.mu.RUnlock()
	
	allInstances := make([]*Instance, 0)
	for _, rt := range runtimes {
		instances, err := rt.List(ctx)
		if err != nil {
			logger.Warn("Failed to list from %s: %v", rt.Name(), err)
			continue
		}
		allInstances = append(allInstances, instances...)
	}
	
	return allInstances, nil
}

// StartBackgroundTasks starts background maintenance tasks.
func (m *Manager) StartBackgroundTasks() {
	m.wg.Add(1)
	go m.maintenanceLoop()
	logger.Info("Started runtime manager background tasks")
}

// Close shuts down the manager.
func (m *Manager) Close() error {
	close(m.stopCh)
	m.wg.Wait()
	logger.Info("Runtime manager shut down")
	return nil
}

func (m *Manager) findInstanceRuntime(ctx context.Context, instanceID string) (Runtime, *Instance, error) {
	m.mu.RLock()
	runtimes := make([]Runtime, 0, len(m.runtimes))
	for _, rt := range m.runtimes {
		runtimes = append(runtimes, rt)
	}
	m.mu.RUnlock()
	
	for _, rt := range runtimes {
		instance, err := rt.Get(ctx, instanceID)
		if err == nil {
			return rt, instance, nil
		}
	}
	
	return nil, nil, fmt.Errorf("instance %s not found", instanceID)
}

func (m *Manager) maintenanceLoop() {
	defer m.wg.Done()
	
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			// Periodic maintenance
		case <-m.stopCh:
			return
		}
	}
}

// Run creates and starts a model instance (legacy API compatibility).
//
// This method bridges the legacy API to the new runtime system. It:
//   1. Determines the runtime name from backend type and deployment mode
//   2. Allocates devices for the instance
//   3. Creates the instance via the appropriate runtime
//   4. Starts the instance
//
// Parameters:
//   - configDir: Configuration directory for storing allocation state
//   - opts: Legacy run options from API handler
//
// Returns:
//   - RunInstance with instance metadata
//   - Error if any step fails
func (m *Manager) Run(configDir string, opts *RunOptions) (*RunInstance, error) {
	if opts == nil {
		return nil, fmt.Errorf("run options cannot be nil")
	}
	
	// Check if an instance of this model is already running
	// Following ollama's design: one model = one instance
	ctx := context.Background()
	instances, err := m.List(ctx)
	if err != nil {
		logger.Warn("Failed to check existing instances: %v", err)
	} else {
		for _, inst := range instances {
			if inst.ModelID == opts.ModelID && inst.State == StateRunning {
				logger.Info("Model %s is already running (instance: %s), returning existing instance", 
					opts.ModelID, inst.ID)
				
				// Return the existing instance as RunInstance
				return &RunInstance{
					ID:             inst.ID,
					ModelID:        inst.ModelID,
					BackendType:    inst.Metadata["backend_type"],
					DeploymentMode: inst.Metadata["deployment_mode"],
					State:          inst.State,
					CreatedAt:      inst.CreatedAt,
					StartedAt:      inst.StartedAt,
					Port:           inst.Port,
					Error:          inst.Error,
				}, nil
			}
		}
	}
	
	// Determine runtime name from backend type + deployment mode
	// Format: "{backend}-{mode}", e.g., "vllm-docker", "mindie-docker"
	runtimeName := fmt.Sprintf("%s-%s", opts.BackendType, opts.DeploymentMode)
	
	// Get the runtime
	m.mu.RLock()
	rt, exists := m.runtimes[runtimeName]
	m.mu.RUnlock()
	
	if !exists {
		return nil, fmt.Errorf("runtime %s not available", runtimeName)
}

	// Generate unique instance ID
	instanceID := fmt.Sprintf("%s-%d", opts.ModelID, time.Now().Unix())
	
	// Validate model path
	if opts.ModelPath == "" {
		return nil, fmt.Errorf("model path is required")
	}
	
	// Get or create device allocator
	allocator, err := m.getOrCreateAllocator(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize device allocator: %w", err)
	}
	
	var devices []DeviceInfo
	
	// Check if specific devices were requested via --device parameter
	if deviceList, ok := opts.AdditionalConfig["device"].(string); ok && deviceList != "" {
		// User specified devices explicitly (e.g., "0" or "0,1,2,3")
		// Parse the device list and use those specific devices
		deviceIndices, err := parseDeviceList(deviceList)
		if err != nil {
			return nil, fmt.Errorf("invalid device list: %w", err)
		}
		
		// Get all devices from the system
		allDevices := allocator.GetAllDevices()
		
		// Select the requested devices
		devices = make([]DeviceInfo, 0, len(deviceIndices))
		for _, idx := range deviceIndices {
			if idx >= len(allDevices) {
				return nil, fmt.Errorf("device index %d out of range (available: %d devices)", idx, len(allDevices))
			}
			dev := allDevices[idx]
			devices = append(devices, DeviceInfo{
				Type:       api.DeviceType(dev.Type),
				Index:      dev.Index,
				PCIAddress: dev.BusAddress,
				ModelName:  dev.ModelName,
				Properties: dev.Properties,
			})
		}
		
		logger.Info("Using user-specified devices: %v", deviceIndices)
	} else {
		// No specific devices requested - use automatic allocation
		// Always allocate 1 device by default
		deviceCount := 1
		
		allocatedDevices, err := allocator.Allocate(instanceID, deviceCount)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate devices: %w", err)
		}
		
		// Convert device.DeviceInfo to runtime.DeviceInfo
		devices = make([]DeviceInfo, len(allocatedDevices))
		for i, dev := range allocatedDevices {
			devices[i] = DeviceInfo{
				Type:       api.DeviceType(dev.Type),
				Index:      dev.Index,
				PCIAddress: dev.BusAddress,
				ModelName:  dev.ModelName,
				Properties: dev.Properties,
			}
		}
		
		logger.Info("Auto-allocated %d device(s) for instance %s", deviceCount, instanceID)
	}

	// Prepare create parameters
	extraConfig := make(map[string]interface{})
	for k, v := range opts.AdditionalConfig {
		extraConfig[k] = v
	}
	
	params := &CreateParams{
		InstanceID:     instanceID,
		ModelID:        opts.ModelID,
		ModelPath:      opts.ModelPath,
		ModelVersion:   "latest",
		BackendType:    opts.BackendType,    // Pass backend type
		DeploymentMode: opts.DeploymentMode, // Pass deployment mode
		Devices:        devices,
		Port:           opts.Port,
		Environment:    make(map[string]string),
		ExtraConfig:    extraConfig,
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	
	// Create the instance
	instance, err := rt.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create instance: %w", err)
	}
	
	// Start the instance
	if err := rt.Start(ctx, instanceID); err != nil {
		// Clean up on failure
		_ = rt.Remove(context.Background(), instanceID)
		_ = allocator.Release(instanceID) // Release allocated devices
		return nil, fmt.Errorf("failed to start instance: %w", err)
	}
	
	// Convert to RunInstance for legacy API
	runInstance := &RunInstance{
		ID:             instance.ID,
		ModelID:        instance.ModelID,
		BackendType:    opts.BackendType,
		DeploymentMode: opts.DeploymentMode,
		State:          instance.State,
		CreatedAt:      instance.CreatedAt,
		StartedAt:      instance.StartedAt,
		Port:           instance.Port,
		Error:          instance.Error,
		Config:         opts.AdditionalConfig,
	}
	
	return runInstance, nil
}

func (m *Manager) ListCompat() []*RunInstance {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	instances, err := m.List(ctx)
	if err != nil {
		return []*RunInstance{}
	}
	
	result := make([]*RunInstance, 0, len(instances))
	for _, inst := range instances {
		result = append(result, &RunInstance{
			ID:             inst.ID,
			ModelID:        inst.ModelID,
			BackendType:    inst.Metadata["backend_type"],    // Read from metadata
			DeploymentMode: inst.Metadata["deployment_mode"], // Read from metadata
			State:          inst.State,
			CreatedAt:      inst.CreatedAt,
			StartedAt:      inst.StartedAt,
			Port:           inst.Port,
			Error:          inst.Error,
		})
	}
	return result
}

func (m *Manager) StopCompat(instanceID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return m.Stop(ctx, instanceID)
}

// parseDeviceList parses a device list string like "0" or "0,1,2,3" into device indices.
//
// Parameters:
//   - deviceList: Comma-separated list of device indices (e.g., "0", "0,1,2,3")
//
// Returns:
//   - Array of device indices
//   - Error if parsing fails
func parseDeviceList(deviceList string) ([]int, error) {
	deviceList = strings.TrimSpace(deviceList)
	if deviceList == "" {
		return nil, fmt.Errorf("empty device list")
	}
	
	parts := strings.Split(deviceList, ",")
	indices := make([]int, 0, len(parts))
	
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		
		idx, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid device index '%s': %w", part, err)
		}
		
		if idx < 0 {
			return nil, fmt.Errorf("device index cannot be negative: %d", idx)
		}
		
		indices = append(indices, idx)
	}
	
	if len(indices) == 0 {
		return nil, fmt.Errorf("no valid device indices found")
	}
	
	return indices, nil
}
