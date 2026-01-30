// Package runtime provides runtime management for model instances.
//
// This package implements a manager that coordinates multiple runtime
// implementations (e.g., vLLM-Docker, MindIE-Docker), handles device
// allocation, and provides lifecycle management for model instances.
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
	"github.com/tsingmao/xw/internal/models"
)

// Manager manages multiple runtime implementations.
type Manager struct {
	mu              sync.RWMutex
	runtimes        map[string]Runtime
	deviceAllocator *device.Allocator // Lazy-initialized device allocator
	stopCh          chan struct{}
	wg              sync.WaitGroup
	serverName      string // Server unique identifier for multi-server support
}

// NewManager creates a new runtime manager with the given server name.
// The server name is used as a suffix for container names to support multiple xw servers.
func NewManager(serverName string) (*Manager, error) {
	return &Manager{
		runtimes:        make(map[string]Runtime),
		deviceAllocator: nil, // Lazy-initialized on first use
		stopCh:          make(chan struct{}),
		serverName:      serverName,
	}, nil
}

// GetServerName returns the server name
func (m *Manager) GetServerName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.serverName
}

// SetServerName sets the server name (used during initialization)
func (m *Manager) SetServerName(name string) {
	m.mu.Lock()
	m.serverName = name
	
	// Update server name in all runtimes
	for _, rt := range m.runtimes {
		if vllmRT, ok := rt.(interface{ SetServerName(string) }); ok {
			vllmRT.SetServerName(name)
		}
	}
	m.mu.Unlock()
	
	// Reload containers from Docker after setting server name
	for _, rt := range m.runtimes {
		if reloadable, ok := rt.(interface{ ReloadContainers(context.Context) error }); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := reloadable.ReloadContainers(ctx); err != nil {
				logger.Warn("Failed to reload containers for runtime: %v", err)
			}
			cancel()
		}
	}
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

// findInstanceRuntime searches all registered runtimes for an instance.
//
// This method iterates through all runtimes to find the one that manages
// the specified instance.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - instanceID: ID of the instance to find
//
// Returns:
//   - Runtime that manages the instance
//   - Instance metadata
//   - Error if instance not found
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

// maintenanceLoop runs periodic maintenance tasks in the background.
//
// This goroutine performs periodic maintenance such as checking instance
// health, cleaning up stale resources, etc. It runs until the manager
// is closed.
func (m *Manager) maintenanceLoop() {
	defer m.wg.Done()
	
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			// Periodic maintenance tasks
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
	
	// Set default alias to model ID if not specified
	if opts.Alias == "" {
		opts.Alias = opts.ModelID
	}
	
	// Check if alias conflicts with registered model IDs
	if opts.Alias != opts.ModelID {
		// Check if alias matches a model ID
		// This prevents confusion where alias could be mistaken for a real model
		spec := models.GetModelSpec(opts.Alias)
		if spec != nil {
			return nil, fmt.Errorf("alias '%s' conflicts with an existing model ID, please choose a different alias", opts.Alias)
		}
	}
	
	// Check if an instance with this alias already exists
	ctx := context.Background()
	instances, err := m.List(ctx)
	if err != nil {
		logger.Warn("Failed to check existing instances: %v", err)
	} else {
		for _, inst := range instances {
			existingAlias := inst.Alias
			if existingAlias == "" {
				existingAlias = inst.ModelID // Backward compatibility
			}
			
			if existingAlias == opts.Alias {
				// Found instance with same alias
				if inst.State == StateRunning {
					// Already running - error
					return nil, fmt.Errorf("alias '%s' is already running. Stop it first with 'xw stop %s' or use a different --alias", 
						opts.Alias, opts.Alias)
				} else if inst.State == StateStopped {
					// Stopped - restart it
					logger.Info("Found stopped instance with alias '%s', restarting it", opts.Alias)
					
					// Get the runtime for this instance
					runtimeName := inst.RuntimeName
					m.mu.RLock()
					rt, exists := m.runtimes[runtimeName]
					m.mu.RUnlock()
					
					if !exists {
						return nil, fmt.Errorf("runtime %s not available for instance", runtimeName)
					}
					
					// Start the instance
					startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cancel()
					
					if err := rt.Start(startCtx, inst.ID); err != nil {
						return nil, fmt.Errorf("failed to start existing instance: %w", err)
					}
					
					// Refresh instance data
					refreshedInst, err := rt.Get(startCtx, inst.ID)
					if err != nil {
						return nil, fmt.Errorf("failed to get instance after start: %w", err)
					}
					
					// Return the started instance
				return &RunInstance{
						ID:             refreshedInst.ID,
						ModelID:        refreshedInst.ModelID,
						Alias:          refreshedInst.Alias,
						BackendType:    refreshedInst.Metadata["backend_type"],
						DeploymentMode: refreshedInst.Metadata["deployment_mode"],
						State:          refreshedInst.State,
						CreatedAt:      refreshedInst.CreatedAt,
						StartedAt:      refreshedInst.StartedAt,
						Port:           refreshedInst.Port,
						Error:          refreshedInst.Error,
						Config:         opts.AdditionalConfig,
				}, nil
				}
			}
		}
	}
	
	// No existing instance with this alias - create new one
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
	// If alias is set, use it as the instance ID; otherwise generate with timestamp
	instanceID := opts.Alias
	if instanceID == "" {
		instanceID = fmt.Sprintf("%s-%d", opts.ModelID, time.Now().Unix())
	}
	
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
				ConfigKey:  dev.ConfigKey,
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
				ConfigKey:  dev.ConfigKey,
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
		Alias:          opts.Alias,
		ModelPath:      opts.ModelPath,
		ModelVersion:   "latest",
		BackendType:    opts.BackendType,    // Pass backend type
		DeploymentMode: opts.DeploymentMode, // Pass deployment mode
		ServerName:     m.serverName,        // Pass server name for container naming
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
		return nil, err
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
		Alias:          instance.Alias,
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

// ListCompat lists all instances in legacy API format.
//
// This method provides backward compatibility with the legacy API by
// converting instances to the RunInstance format.
//
// Returns:
//   - Array of RunInstance objects
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
			Alias:          inst.Alias,
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

// StopCompat stops an instance with legacy API compatibility.
//
// This method provides backward compatibility by wrapping the Stop
// method with a timeout.
//
// Parameters:
//   - instanceID: ID of the instance to stop
//
// Returns:
//   - Error if stop fails
func (m *Manager) StopCompat(instanceID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return m.Stop(ctx, instanceID)
}

// RemoveCompat removes an instance with legacy API compatibility.
//
// This method provides backward compatibility by wrapping the Remove
// method with a timeout. If force is true, it stops the instance
// before removing it.
//
// Parameters:
//   - instanceID: ID of the instance to remove
//   - force: If true, stops the instance before removing
//
// Returns:
//   - Error if remove fails
func (m *Manager) RemoveCompat(instanceID string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	// If force is true, stop the instance first
	if force {
		// Ignore errors from stop - instance might already be stopped
		_ = m.Stop(ctx, instanceID)
	}
	
	return m.Remove(ctx, instanceID)
}

// findInstanceByAlias searches for an instance by its alias.
//
// This method searches all instances and matches by alias. For backward
// compatibility, if an instance has no alias, it falls back to using
// the ModelID.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - alias: The alias to search for
//
// Returns:
//   - Instance metadata if found
//   - Error if instance not found or search fails
func (m *Manager) findInstanceByAlias(ctx context.Context, alias string) (*Instance, error) {
	instances, err := m.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}
	
	for _, inst := range instances {
		instAlias := inst.Alias
		if instAlias == "" {
			instAlias = inst.ModelID // Backward compatibility
		}
		if instAlias == alias {
			return inst, nil
		}
	}
	
	return nil, fmt.Errorf("instance with alias '%s' not found", alias)
}

// StopByAliasCompat stops an instance by its alias.
//
// This method provides a convenient way to stop instances using their
// alias instead of the internal instance ID.
//
// Parameters:
//   - alias: The alias of the instance to stop
//
// Returns:
//   - Error if the instance is not found or stop fails
func (m *Manager) StopByAliasCompat(alias string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	// Find instance by alias
	inst, err := m.findInstanceByAlias(ctx, alias)
	if err != nil {
		return err
	}
	
	return m.Stop(ctx, inst.ID)
}

// RemoveByAliasCompat removes an instance by its alias.
//
// This method provides a convenient way to remove instances using their
// alias instead of the internal instance ID. If force is true, it stops
// the instance before removing it.
//
// Parameters:
//   - alias: The alias of the instance to remove
//   - force: If true, stops the instance before removing
//
// Returns:
//   - Error if the instance is not found or remove fails
func (m *Manager) RemoveByAliasCompat(alias string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	// Find instance by alias
	inst, err := m.findInstanceByAlias(ctx, alias)
	if err != nil {
		return err
	}
	
	// If force is true, stop the instance first
	if force {
		// Ignore errors from stop - instance might already be stopped
		_ = m.Stop(ctx, inst.ID)
	}
	
	return m.Remove(ctx, inst.ID)
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

// GetLogsByAlias retrieves the log stream for an instance by its alias.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - alias: Instance alias
//   - follow: If true, stream logs in real-time
//
// Returns:
//   - LogStream reader
//   - Error if instance not found
func (m *Manager) GetLogsByAlias(ctx context.Context, alias string, follow bool) (LogStream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	// Find instance by alias
	instance, err := m.findInstanceByAlias(ctx, alias)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, fmt.Errorf("instance with alias '%s' not found", alias)
	}
	
	// Get the runtime
	runtimeName := instance.RuntimeName
	rt, exists := m.runtimes[runtimeName]
	if !exists {
		return nil, fmt.Errorf("runtime %s not found", runtimeName)
	}
	
	// Get logs from runtime
	return rt.Logs(ctx, instance.ID, follow)
}
