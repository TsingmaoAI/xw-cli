// Package runtime provides runtime management for model instances.
package runtime

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	runtimePkg "runtime"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// CreateContainerWithLabels creates a Docker container with automatic common label injection.
//
// This method wraps Docker's ContainerCreate API and automatically adds common xw labels
// to ensure all containers can be discovered and managed consistently.
//
// Common labels added automatically:
//   - xw.runtime: Runtime type (e.g., "vllm-docker", "mindie-docker")
//   - xw.model_id: Model identifier
//   - xw.alias: Instance alias for inference
//   - xw.instance_id: Unique instance identifier
//   - xw.backend_type: Backend type (e.g., "vllm", "mindie")
//   - xw.deployment_mode: Deployment mode (e.g., "docker")
//   - xw.server_name: Server identifier for multi-server support
//   - xw.max_concurrent: Max concurrent requests (if specified in ExtraConfig)
//
// Runtime-specific labels can be passed via the extraLabels parameter.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - params: Creation parameters containing model info and configuration
//   - containerConfig: Docker container configuration
//   - hostConfig: Docker host configuration
//   - containerName: Name for the container
//   - extraLabels: Additional runtime-specific labels (optional)
//
// Returns:
//   - Container creation response with ID
//   - Error if container creation fails
func (b *DockerRuntimeBase) CreateContainerWithLabels(
	ctx context.Context,
	params *CreateParams,
	containerConfig *container.Config,
	hostConfig *container.HostConfig,
	containerName string,
	extraLabels map[string]string,
) (container.CreateResponse, error) {
	// Prepare common labels
	commonLabels := map[string]string{
		"xw.runtime":         b.runtimeName,
		"xw.model_id":        params.ModelID,
		"xw.alias":           params.Alias,
		"xw.instance_id":     params.InstanceID,
		"xw.backend_type":    params.BackendType,
		"xw.deployment_mode": params.DeploymentMode,
		"xw.server_name":     params.ServerName,
	}
	
	// Add max_concurrent label if specified (used by proxy for concurrency control)
	if maxConcurrent, ok := params.ExtraConfig["max_concurrent"].(int); ok && maxConcurrent > 0 {
		commonLabels["xw.max_concurrent"] = fmt.Sprintf("%d", maxConcurrent)
	}
	
	// Merge common labels with extra labels (extra labels can override if needed)
	if containerConfig.Labels == nil {
		containerConfig.Labels = make(map[string]string)
	}
	
	for k, v := range commonLabels {
		containerConfig.Labels[k] = v
	}
	
	for k, v := range extraLabels {
		containerConfig.Labels[k] = v
	}
	
	// Create container via Docker API
	return b.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, containerName)
}

// DockerRuntimeBase provides common Docker operations for runtime implementations.
//
// This base implementation handles the shared Docker infrastructure used by
// different runtime backends (vLLM, MindIE, etc.). It provides:
//   - Docker client lifecycle management with connection pooling
//   - Container lifecycle operations (start, stop, remove)
//   - Instance state tracking and synchronization
//   - Log streaming with proper cleanup
//   - Container discovery and restoration after restarts
//
// Concrete runtime implementations should embed this struct and implement
// the Create() method with their specific container configuration logic.
//
// Thread Safety:
//   All methods are thread-safe through RWMutex synchronization.
//   The instances map is protected for concurrent access by multiple goroutines.
type DockerRuntimeBase struct {
	client     *client.Client          // Docker API client with version negotiation
	mu         sync.RWMutex            // Protects instances map and serverName
	instances  map[string]*Instance    // Active instances indexed by ID
	serverName string                  // Server identifier for multi-server deployments
	runtimeName string                 // Runtime type name (e.g., "vllm-docker", "mindie-docker")
	
	// Unified sandbox management (configuration-first design)
	// Extended sandboxes (from config) have higher priority than core sandboxes (code)
	// This allows users to override default implementations via configuration
	coreSandboxes []func() DeviceSandbox  // Core sandboxes registered by runtime
	extSandboxes  []func() DeviceSandbox  // Extended sandboxes loaded from config
	sandboxOnce   sync.Once               // Ensures extended sandboxes loaded once
}

// NewDockerRuntimeBase creates and initializes a new Docker runtime base.
//
// This function performs the following initialization steps:
//   1. Creates Docker client with environment-based configuration (DOCKER_HOST, etc.)
//   2. Negotiates API version with Docker daemon for compatibility
//   3. Verifies Docker daemon connectivity with timeout
//   4. Initializes instance tracking structures
//
// The created base must be embedded in a concrete runtime implementation.
// Call LoadExistingContainers() after construction to restore previous state.
//
// Parameters:
//   - runtimeName: Unique identifier for this runtime type (used in container labels)
//
// Returns:
//   - Initialized base runtime instance
//   - Error if Docker daemon is unreachable or client creation fails
//
// Example:
//   base, err := NewDockerRuntimeBase("vllm-docker")
//   if err != nil {
//       return nil, fmt.Errorf("failed to initialize: %w", err)
//   }
func NewDockerRuntimeBase(runtimeName string) (*DockerRuntimeBase, error) {
	if runtimeName == "" {
		return nil, fmt.Errorf("runtime name is required")
	}

	// Create Docker client with environment-based configuration
	// This respects DOCKER_HOST, DOCKER_TLS_VERIFY, DOCKER_CERT_PATH environment variables
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Verify Docker daemon connectivity with 5-second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("Docker daemon is not accessible: %w", err)
	}

	base := &DockerRuntimeBase{
		client:      cli,
		instances:   make(map[string]*Instance),
		runtimeName: runtimeName,
	}

	logger.Info("Docker runtime base initialized: %s", runtimeName)

	return base, nil
}

// SetServerName configures the server name for multi-server support.
//
// The server name is used as a suffix in container names and as a filter
// when loading existing containers. This allows multiple xw servers to
// coexist on the same Docker host without conflicts.
//
// This method should be called before LoadExistingContainers() to ensure
// proper container filtering.
//
// Parameters:
//   - name: Unique server identifier (e.g., hostname, UUID)
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) SetServerName(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.serverName = name
	logger.Debug("Server name set to: %s for runtime: %s", name, b.runtimeName)
}

// GetServerName returns the current server name.
//
// Returns:
//   - Server name string, or empty if not configured
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) GetServerName() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.serverName
}

// RegisterCoreSandboxes registers core device sandboxes for this runtime.
//
// This method should be called during runtime initialization to register
// code-based sandbox implementations for mainstream accelerators.
//
// Core sandboxes serve as fallbacks when no configuration-based sandboxes
// are found for a device type. This design allows users to override default
// implementations via configuration.
//
// Parameters:
//   - sandboxes: List of sandbox constructor functions
//
// Thread Safety: Should be called during initialization before concurrent access
//
// Example:
//
//	base.RegisterCoreSandboxes([]func() runtime.DeviceSandbox{
//	    func() runtime.DeviceSandbox { return NewAscendSandbox() },
//	    func() runtime.DeviceSandbox { return NewMetaXSandbox() },
//	})
func (b *DockerRuntimeBase) RegisterCoreSandboxes(sandboxes []func() DeviceSandbox) {
	b.coreSandboxes = sandboxes
	logger.Debug("Registered %d core sandbox(es) for %s", len(sandboxes), b.runtimeName)
}

// SelectSandbox selects an appropriate sandbox for the given device type.
//
// This method implements a configuration-first selection strategy:
//  1. Check extended sandboxes from configuration (higher priority)
//  2. Fall back to core sandboxes from code (lower priority)
//
// Extended sandboxes are loaded lazily on first call to avoid timing issues
// with configuration file loading.
//
// Parameters:
//   - deviceType: Device config_key to find sandbox for (e.g., "ascend-910b")
//
// Returns:
//   - DeviceSandbox instance if found
//   - Error if no sandbox supports the device type
//
// Thread Safety: Safe for concurrent calls
//
// Example:
//
//	sandbox, err := base.SelectSandbox("ascend-910b")
//	if err != nil {
//	    return fmt.Errorf("no sandbox for device: %w", err)
//	}
func (b *DockerRuntimeBase) SelectSandbox(deviceType string) (DeviceSandbox, error) {
	// Lazy load extended sandboxes from configuration
	b.loadExtendedSandboxes()
	
	// Priority 1: Try extended sandboxes from configuration
	// These have higher priority as they represent explicit user configuration
	for _, constructor := range b.extSandboxes {
		sb := constructor()
		if sb.Supports(deviceType) {
			logger.Debug("Selected extended sandbox for %s: %T", deviceType, sb)
			return sb, nil
		}
	}
	
	// Priority 2: Fall back to core sandboxes from code
	// These are default implementations for mainstream accelerators
	for _, constructor := range b.coreSandboxes {
		sb := constructor()
		if sb.Supports(deviceType) {
			logger.Debug("Selected core sandbox for %s: %T", deviceType, sb)
			return sb, nil
		}
	}
	
	return nil, fmt.Errorf("no sandbox found for device type: %s", deviceType)
}

// loadExtendedSandboxes loads extended sandboxes from device configuration.
//
// This method uses sync.Once to ensure sandboxes are only loaded once,
// even under concurrent access. Loading is deferred until first sandbox
// selection to avoid timing issues with configuration file initialization.
//
// The method extracts the engine name from the runtime name (e.g., "vllm-docker" -> "vllm")
// and loads corresponding sandbox configurations from devices.yaml.
//
// Thread Safety: Safe for concurrent calls (protected by sync.Once)
func (b *DockerRuntimeBase) loadExtendedSandboxes() {
	b.sandboxOnce.Do(func() {
		// Extract engine name from runtime name (e.g., "vllm-docker" -> "vllm")
		engineName := b.runtimeName
		if idx := strings.Index(engineName, "-"); idx > 0 {
			engineName = engineName[:idx]
		}
		
		// Load extended sandboxes for this engine
		b.extSandboxes = LoadExtendedSandboxes(engineName)
		
		if len(b.extSandboxes) > 0 {
			logger.Info("Loaded %d extended sandbox(es) for %s", len(b.extSandboxes), b.runtimeName)
		}
	})
}

// Start starts a created Docker container instance.
//
// This method transitions a container from "created" or "stopped" state to "running".
// The container begins executing its configured entrypoint/command.
//
// The method performs the following:
//   1. Validates instance exists in tracking map
//   2. Extracts container ID from instance metadata
//   3. Issues Docker start command
//   4. Updates instance state and start timestamp
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - instanceID: Unique identifier of the instance to start
//
// Returns:
//   - nil on success
//   - Error if instance not found, container ID missing, or Docker operation fails
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) Start(ctx context.Context, instanceID string) error {
	b.mu.RLock()
	instance, exists := b.instances[instanceID]
	b.mu.RUnlock()

	if !exists {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	containerID := instance.Metadata["container_id"]
	if containerID == "" {
		return fmt.Errorf("container ID not found for instance: %s", instanceID)
	}

	logger.Info("Starting Docker container: %s (instance: %s)", containerID[:12], instanceID)

	if err := b.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Update instance state atomically
	b.mu.Lock()
	instance.State = StateStarting
	instance.StartedAt = time.Now()
	b.mu.Unlock()

	logger.Info("Docker container started successfully: %s", instanceID)

	return nil
}

// Stop gracefully stops a running Docker container.
//
// This method sends SIGTERM to the container and waits up to 30 seconds
// for graceful shutdown. If the container doesn't stop within the timeout,
// Docker will send SIGKILL to force termination.
//
// The 30-second timeout allows models to complete any in-flight
// inference requests and perform proper cleanup before shutdown.
//
// After stopping, the container is preserved (not removed) to allow:
//   - Inspection of final state and logs
//   - Quick restart without recreating container
//   - Manual cleanup decision by operators
//
// Parameters:
//   - ctx: Context for cancellation (separate from container stop timeout)
//   - instanceID: Unique identifier of the instance to stop
//
// Returns:
//   - nil on success
//   - Error if instance not found or Docker operation fails
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) Stop(ctx context.Context, instanceID string) error {
	b.mu.RLock()
	instance, exists := b.instances[instanceID]
	b.mu.RUnlock()

	if !exists {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	containerID := instance.Metadata["container_id"]
	logger.Info("Stopping Docker container: %s (instance: %s)", containerID[:12], instanceID)

	// Configure graceful shutdown with 30-second timeout
	// After timeout, Docker sends SIGKILL to force termination
	timeout := 30
	stopOptions := container.StopOptions{Timeout: &timeout}

	if err := b.client.ContainerStop(ctx, containerID, stopOptions); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	// Update state to stopped (preserve instance for restart or removal)
	b.mu.Lock()
	instance.State = StateStopped
	instance.StoppedAt = time.Now()
	b.mu.Unlock()

	logger.Info("Docker container stopped successfully: %s (container preserved)", instanceID)

	return nil
}

// Remove permanently removes a Docker container and its instance tracking.
//
// This method performs the following cleanup:
//   1. Force stops the container if still running
//   2. Removes the container and its anonymous volumes
//   3. Unregisters instance from tracking map
//   4. Releases associated resources
//
// The operation is idempotent - removing an already-removed container
// will return an error from Docker but won't corrupt state.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - instanceID: Unique identifier of the instance to remove
//
// Returns:
//   - nil on success
//   - Error if instance not found or Docker operation fails
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) Remove(ctx context.Context, instanceID string) error {
	b.mu.RLock()
	instance, exists := b.instances[instanceID]
	b.mu.RUnlock()

	if !exists {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	containerID := instance.Metadata["container_id"]
	logger.Info("Removing Docker container: %s (instance: %s)", containerID[:12], instanceID)

	removeOptions := container.RemoveOptions{
		Force:         true, // Force remove even if running
		RemoveVolumes: true, // Clean up anonymous volumes to prevent disk leaks
	}

	if err := b.client.ContainerRemove(ctx, containerID, removeOptions); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	// Unregister instance atomically
	b.mu.Lock()
	delete(b.instances, instanceID)
	b.mu.Unlock()

	logger.Info("Docker container removed successfully: %s", instanceID)

	return nil
}

// Get retrieves instance information by ID.
//
// This method returns a pointer to the instance struct, allowing callers
// to read instance metadata, state, and configuration. The returned pointer
// should not be modified directly - use runtime methods for state changes.
//
// This method also checks the actual container status and updates instance state
// if the container has exited unexpectedly.
//
// Parameters:
//   - ctx: Context for cancellation
//   - instanceID: Unique identifier of the instance to retrieve
//
// Returns:
//   - Instance pointer on success
//   - Error if instance not found
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) Get(ctx context.Context, instanceID string) (*Instance, error) {
	b.mu.RLock()
	instance, exists := b.instances[instanceID]
	b.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}

	// Check actual container status and update state if needed
	b.updateInstanceStateFromContainer(ctx, instance)

	return instance, nil
}

// updateInstanceStateFromContainer checks the actual container status and updates
// instance state if the container has exited unexpectedly.
//
// This method should be called when querying instance status to ensure the
// reported state matches the actual container state. It only checks containers
// that are supposed to be running (StateStarting, StateRunning, StateReady).
//
// If the container has exited, the instance state is updated to StateUnhealthy
// and the exit code and error message are logged.
//
// Parameters:
//   - ctx: Context for cancellation
//   - inst: Instance to check
//
// Thread Safety: Acquires lock when updating state
// updateInstanceStateFromContainer synchronizes instance state with actual container state.
//
// This method uses the centralized state management module to ensure consistent
// state mapping across the codebase. It only updates instances in active states
// (starting, running, ready) to detect unexpected exits.
//
// Thread Safety: This method acquires the mutex if state change is needed
func (b *DockerRuntimeBase) updateInstanceStateFromContainer(ctx context.Context, inst *Instance) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	// Use centralized state management - pass instance by value to check without holding lock
	// Note: We hold the lock for the entire operation to ensure atomicity
	UpdateInstanceStateFromContainer(ctx, b.client, inst)
}

// List returns all instances managed by this runtime.
//
// The returned slice contains pointers to all tracked instances, regardless
// of their state (running, stopped, etc.). The list is a snapshot at call time.
//
// This method also checks the actual container status and updates instance state
// if the container has exited unexpectedly (e.g., due to errors).
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - Slice of instance pointers (empty if no instances)
//   - Error (currently always nil, reserved for future use)
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) List(ctx context.Context) ([]*Instance, error) {
	// Query Docker directly for all containers with our runtime label
	// This ensures we always return the latest state without relying on memory cache
	containers, err := b.client.ContainerList(ctx, container.ListOptions{
		All: true, // Include stopped containers
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("xw.runtime=%s", b.runtimeName)),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	instancesList := make([]*Instance, 0, len(containers))
	
	for _, c := range containers {
		instanceID := c.Labels["xw.instance_id"]
		if instanceID == "" {
			continue
		}

		// Filter by server name if configured (for multi-server support)
		if b.serverName != "" {
			containerServerName := c.Labels["xw.server_name"]
			if containerServerName != b.serverName {
				continue
			}
		}

		// Inspect container to get detailed state
		stateInfo, err := InspectContainerState(ctx, b.client, c.ID)
		if err != nil {
			logger.Debug("Failed to inspect container %s during list: %v", c.ID[:12], err)
			stateInfo = &ContainerStateInfo{
				State:        StateUnknown,
				ErrorMessage: fmt.Sprintf("Failed to inspect: %v", err),
			}
		}

		// Extract port mapping
		port := 0
		for _, portMapping := range c.Ports {
			if portMapping.PrivatePort == 8000 {
				port = int(portMapping.PublicPort)
				break
			}
		}

		// Convert timestamps
		createdAt := time.Unix(c.Created, 0)
		startedAt := createdAt
		if stateInfo.IsRunning {
			if inspectData, err := b.client.ContainerInspect(ctx, c.ID); err == nil {
				if inspectData.State != nil && inspectData.State.StartedAt != "" {
					if parsedTime, err := time.Parse(time.RFC3339Nano, inspectData.State.StartedAt); err == nil {
						startedAt = parsedTime
					}
				}
			}
		}

		// Build instance from container info
		metadata := map[string]string{
			"container_id":    c.ID,
			"backend_type":    c.Labels["xw.backend_type"],
			"deployment_mode": c.Labels["xw.deployment_mode"],
		}
		
		// Copy max_concurrent from label if present
		if maxConcurrent := c.Labels["xw.max_concurrent"]; maxConcurrent != "" {
			metadata["max_concurrent"] = maxConcurrent
		}

		instance := &Instance{
			ID:          instanceID,
			RuntimeName: b.runtimeName,
			ModelID:     c.Labels["xw.model_id"],
			Alias:       c.Labels["xw.alias"],
			State:       stateInfo.State,
			Port:        port,
			CreatedAt:   createdAt,
			StartedAt:   startedAt,
			Metadata:    metadata,
			Error:       stateInfo.ErrorMessage,
		}

		instancesList = append(instancesList, instance)
	}

	return instancesList, nil
}

// Logs retrieves container logs for an instance.
//
// This method streams logs from the Docker container with the following options:
//   - Both stdout and stderr are included
//   - Timestamps are prepended to each log line
//   - All historical logs are returned ("tail=all")
//   - Optionally follows new logs in real-time
//
// The returned LogStream must be closed by the caller to release resources.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - instanceID: Unique identifier of the instance
//   - follow: If true, stream continues with new logs; if false, return existing logs and close
//
// Returns:
//   - LogStream for reading log data
//   - Error if instance not found or Docker operation fails
//
// Example:
//   stream, err := runtime.Logs(ctx, "my-instance", true)
//   if err != nil {
//       return err
//   }
//   defer stream.Close()
//   io.Copy(os.Stdout, stream)
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) Logs(ctx context.Context, instanceID string, follow bool) (LogStream, error) {
	b.mu.RLock()
	instance, exists := b.instances[instanceID]
	b.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}

	containerID := instance.Metadata["container_id"]
	options := container.LogsOptions{
		ShowStdout: true,  // Include stdout stream
		ShowStderr: true,  // Include stderr stream
		Follow:     follow, // Stream new logs if true
		Timestamps: true,  // Prepend RFC3339Nano timestamps
		Tail:       "all", // Return all historical logs
	}

	reader, err := b.client.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get container logs: %w", err)
	}

	return &dockerLogStream{reader: reader}, nil
}

// LoadExistingContainers discovers and registers containers from previous runs.
//
// This method performs container restoration by:
//   1. Querying Docker for containers with matching runtime label
//   2. Filtering by server name (if configured) for multi-server support
//   3. Inspecting each container using centralized state management
//   4. Extracting container metadata and port mappings
//   5. Marking allocated ports as used to prevent conflicts
//   6. Registering instances in the tracking map
//
// This allows the runtime to resume managing containers after a restart,
// enabling seamless server upgrades and crash recovery.
//
// State Management:
//   Uses InspectContainerState() for consistent state mapping across the codebase.
//   See state_manager.go for detailed state mapping rules.
//
// Port Allocation:
//   Discovered ports are marked as used in the global port allocator to
//   prevent new instances from conflicting with existing ones.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - nil on success
//   - Error if Docker query fails (individual container errors are logged as warnings)
//
// Thread Safety: Safe for concurrent calls (but typically called once during initialization)
func (b *DockerRuntimeBase) LoadExistingContainers(ctx context.Context) error {
	// Query for containers with our runtime label
	containers, err := b.client.ContainerList(ctx, container.ListOptions{
		All: true, // Include stopped containers
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("xw.runtime=%s", b.runtimeName)),
		),
	})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	// Get global port allocator for marking ports as used
	portAllocator := GetGlobalPortAllocator()

	b.mu.Lock()
	defer b.mu.Unlock()

	loadedCount := 0
	for _, c := range containers {
		instanceID := c.Labels["xw.instance_id"]
		if instanceID == "" {
			logger.Warn("Skipping container %s: missing xw.instance_id label", c.ID[:12])
			continue
		}

		// Filter by server name if configured (for multi-server support)
		if b.serverName != "" {
			containerServerName := c.Labels["xw.server_name"]
			if containerServerName != b.serverName {
				logger.Debug("Skipping container %s: belongs to server '%s', not '%s'",
					c.ID[:12], containerServerName, b.serverName)
				continue
			}
		}

		// Inspect container to get detailed state using centralized state management
		stateInfo, err := InspectContainerState(ctx, b.client, c.ID)
		if err != nil {
			logger.Warn("Failed to inspect container %s (instance %s) during load: %v",
				c.ID[:12], instanceID, err)
			// Fallback: use basic state from list
			stateInfo = &ContainerStateInfo{
				State:        StateUnknown,
				ErrorMessage: fmt.Sprintf("Failed to inspect: %v", err),
			}
		}

		// Extract port mapping from container
		// Containers expose port 8000 internally, mapped to a host port
		port := 0
		for _, portMapping := range c.Ports {
			if portMapping.PrivatePort == 8000 {
				port = int(portMapping.PublicPort)
				break
			}
		}

		// Warn if no port mapping found (cannot be accessed via API)
		if port == 0 {
			logger.Warn("Container %s (instance: %s) has no port mapping - "+
				"it cannot be accessed via proxy API. Consider recreating with port allocation.",
				c.ID[:12], instanceID)
		} else {
			// Mark the port as used to prevent conflicts
			portAllocator.MarkPortUsed(port)
		}

		// Convert container creation timestamp
		createdAt := time.Unix(c.Created, 0)

		// Get accurate start time for running containers
		startedAt := createdAt // Default to creation time
		if stateInfo.IsRunning {
			// For running containers, get precise start time
			if inspectData, err := b.client.ContainerInspect(ctx, c.ID); err == nil {
				if inspectData.State != nil && inspectData.State.StartedAt != "" {
					if parsedTime, err := time.Parse(time.RFC3339Nano, inspectData.State.StartedAt); err == nil {
						startedAt = parsedTime
					}
				}
			}
		}

		// Prepare instance metadata
		metadata := map[string]string{
			"container_id":    c.ID,
			"backend_type":    c.Labels["xw.backend_type"],
			"deployment_mode": c.Labels["xw.deployment_mode"],
		}

		instance := &Instance{
			ID:          instanceID,
			RuntimeName: b.runtimeName,
			ModelID:     c.Labels["xw.model_id"],
			Alias:       c.Labels["xw.alias"],
			State:       stateInfo.State,
			Port:        port,
			CreatedAt:   createdAt,
			StartedAt:   startedAt,
			Metadata:    metadata,
			Error:       stateInfo.ErrorMessage,
		}

		b.instances[instanceID] = instance
		loadedCount++

		if stateInfo.ErrorMessage != "" {
			logger.Warn("Loaded container %s (instance %s) in error state: %s [port: %d]",
				c.ID[:12], instanceID, stateInfo.ErrorMessage, port)
		} else {
			logger.Info("Loaded container %s (instance %s) [state: %s, port: %d]",
				c.ID[:12], instanceID, stateInfo.State, port)
		}
	}

	logger.Info("Loaded %d existing containers for runtime: %s", loadedCount, b.runtimeName)

	return nil
}

// ReloadContainers clears and reloads all containers from Docker.
//
// This method is useful when:
//   - Server name changes (multi-server scenarios)
//   - External container modifications need to be detected
//   - Recovering from state inconsistencies
//
// WARNING: This clears all tracked instances and reloads from Docker.
// Any in-memory state not persisted in container labels will be lost.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - nil on success
//   - Error if Docker query fails
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) ReloadContainers(ctx context.Context) error {
	// Clear existing instances
	b.mu.Lock()
	b.instances = make(map[string]*Instance)
	b.mu.Unlock()

	logger.Info("Reloading containers for runtime: %s", b.runtimeName)

	// Reload with current server name filter
	return b.LoadExistingContainers(ctx)
}

// GetDockerClient returns the underlying Docker client.
//
// This method exposes the Docker client for advanced operations not
// covered by the base implementation. Use with caution as direct client
// access bypasses base state management.
//
// Returns:
//   - Docker API client pointer
//
// Thread Safety: Safe for concurrent calls (client itself is thread-safe)
func (b *DockerRuntimeBase) GetDockerClient() *client.Client {
	return b.client
}

// GetInstances returns the instances map for direct access.
//
// This method is intended for concrete runtime implementations that need
// to access or modify the instances map. Callers must handle locking
// appropriately using GetMutex().
//
// WARNING: Direct map access requires external synchronization.
//
// Returns:
//   - Map of instance ID to Instance pointer
//
// Thread Safety: NOT safe - caller must lock GetMutex()
func (b *DockerRuntimeBase) GetInstances() map[string]*Instance {
	return b.instances
}

// GetMutex returns the mutex for synchronizing instance map access.
//
// Concrete runtime implementations should use this mutex when accessing
// the instances map directly via GetInstances().
//
// Returns:
//   - RWMutex pointer for synchronization
//
// Thread Safety: Always safe to call
func (b *DockerRuntimeBase) GetMutex() *sync.RWMutex {
	return &b.mu
}

// dockerLogStream implements LogStream interface for Docker container logs.
//
// This wrapper provides a clean abstraction over Docker's log reader,
// allowing consumers to treat logs as a standard io.ReadCloser.
//
// The Docker log format includes 8-byte headers that multiplex stdout/stderr.
// Consumers may need to handle this format or use Docker's stdcopy package
// to demultiplex streams.
type dockerLogStream struct {
	reader io.ReadCloser
}

// Read implements io.Reader for the log stream.
//
// Reads log data from the underlying Docker container log stream.
// The data includes Docker's multiplexing headers.
//
// Parameters:
//   - p: Buffer to read data into
//
// Returns:
//   - Number of bytes read
//   - Error on read failure or io.EOF when stream ends
func (s *dockerLogStream) Read(p []byte) (n int, err error) {
	return s.reader.Read(p)
}

// Close implements io.Closer for the log stream.
//
// Releases resources associated with the log stream. Must be called
// by consumers to prevent resource leaks.
//
// Returns:
//   - Error if close operation fails
func (s *dockerLogStream) Close() error {
	return s.reader.Close()
}

// DeviceSandbox defines the interface for device-specific configuration.
//
// Each device type (Ascend NPU, Kunlun XPU, NVIDIA GPU, etc.) implements
// this interface to provide chip-specific Docker configuration. This abstraction
// isolates device-specific logic from the core runtime implementation, enabling
// runtime backends to support multiple hardware accelerators.
//
// Responsibilities:
//   - Environment variable preparation (device visibility, logging, etc.)
//   - Device file mounting (e.g., /dev/davinci*, /dev/npu*, /dev/nvidia*)
//   - Additional volume mounts (driver libs, tools, config files)
//   - Security requirements (privileged mode, capabilities)
//   - Docker runtime selection (runc, nvidia, ascend, etc.)
//   - Default container image selection
//
// Implementation Guidelines:
//   - PrepareEnvironment: Return device visibility and configuration variables
//   - GetDeviceMounts: Return device files that need rwm access
//   - GetAdditionalMounts: Return host->container path mappings
//   - RequiresPrivileged: Return true if privileged mode is required
//   - GetCapabilities: Document required Linux capabilities
//   - GetDefaultImage: Return device-optimized container image
//   - GetDockerRuntime: Return Docker runtime name (e.g., "runc", "nvidia")
//
// Thread Safety:
//   Implementations should be stateless and safe for concurrent use.
//
// Example Implementation:
//   type AscendSandbox struct{}
//
//   func (s *AscendSandbox) PrepareEnvironment(devices []DeviceInfo) (map[string]string, error) {
//       return map[string]string{
//           "ASCEND_RT_VISIBLE_DEVICES": "0,1,2,3",
//           "ASCEND_SLOG_PRINT_TO_STDOUT": "1",
//       }, nil
//   }
type DeviceSandbox interface {
	// PrepareEnvironment generates device-specific environment variables.
	//
	// This method prepares the environment configuration that makes devices
	// visible and accessible to processes inside the container. Different
	// device types have different environment variable conventions.
	//
	// Examples:
	//   - Ascend NPU: ASCEND_RT_VISIBLE_DEVICES=0,1,2,3
	//   - NVIDIA GPU: CUDA_VISIBLE_DEVICES=0,1,2,3
	//   - Kunlun XPU: XPU_VISIBLE_DEVICES=0,1,2,3
	//
	// Parameters:
	//   - devices: List of devices to make visible to the container
	//
	// Returns:
	//   - Map of environment variable name to value
	//   - Error if device configuration is invalid
	PrepareEnvironment(devices []DeviceInfo) (map[string]string, error)

	// GetDeviceMounts returns device files that must be mounted into the container.
	//
	// Device files provide direct hardware access to the container. These
	// files are typically under /dev and require special permissions (rwm).
	//
	// Examples:
	//   - Ascend NPU: ["/dev/davinci0", "/dev/davinci_manager", "/dev/devmm_svm"]
	//   - NVIDIA GPU: ["/dev/nvidia0", "/dev/nvidiactl", "/dev/nvidia-uvm"]
	//
	// Parameters:
	//   - devices: List of devices to mount
	//
	// Returns:
	//   - List of device paths (e.g., ["/dev/davinci0", "/dev/davinci_manager"])
	//   - Error if device paths are invalid
	GetDeviceMounts(devices []DeviceInfo) ([]string, error)

	// GetAdditionalMounts returns additional volume mounts required by the device.
	//
	// Many device types require access to host libraries, tools, and configuration
	// files beyond just the device files. This method returns a mapping of host
	// paths to container paths for these additional requirements.
	//
	// Common mount types:
	//   - Driver libraries: Shared libraries required by device SDK
	//   - Management tools: Utilities for device monitoring (npu-smi, nvidia-smi)
	//   - Configuration files: Device installation info and version metadata
	//   - Cache directories: For model downloads and compilation artifacts
	//
	// Returns:
	//   - Map of host path to container path (e.g., {"/usr/local/dcmi": "/usr/local/dcmi"})
	GetAdditionalMounts() map[string]string

	// RequiresPrivileged indicates whether the container needs privileged mode.
	//
	// Privileged mode grants the container extended permissions and capabilities,
	// including access to all devices and the ability to modify system settings.
	// While less secure, some device types require it for proper operation.
	//
	// Security Considerations:
	//   - Privileged containers can potentially access host resources
	//   - Prefer capability-based security when possible
	//   - Document why privileged mode is required if true
	//
	// Returns:
	//   - true if --privileged flag is required
	RequiresPrivileged() bool

	// Supports checks if this sandbox supports the given device type.
	//
	// This method allows sandboxes to declare which device types they support.
	// Runtimes use this to automatically select the appropriate sandbox
	// implementation for a given device type without hardcoding device lists.
	//
	// Each sandbox should explicitly list the device types it has been
	// tested and validated to work with. Device types must match the
	// config_key values from the device configuration (devices.yaml).
	//
	// Parameters:
	//   - deviceType: Device type string (e.g., "ascend-910b", "ascend-310p")
	//
	// Returns:
	//   - true if this sandbox supports the device type
	//
	// Example:
	//
	//	func (s *AscendSandbox) Supports(deviceType string) bool {
	//	    supportedTypes := []string{"ascend-910b", "ascend-310p"}
	//	    for _, t := range supportedTypes {
	//	        if deviceType == t { return true }
	//	    }
	//	    return false
	//	}
	Supports(deviceType string) bool

	// GetCapabilities returns Linux capabilities needed by the container.
	//
	// Linux capabilities provide fine-grained privilege control. Even when
	// privileged mode is used, documenting required capabilities helps
	// understand security requirements and may support future migration
	// to non-privileged mode.
	//
	// Common capabilities:
	//   - SYS_ADMIN: System administration operations
	//   - SYS_RAWIO: Direct device I/O access
	//   - IPC_LOCK: Memory locking for device buffers
	//   - SYS_RESOURCE: Resource limit adjustments
	//
	// Returns:
	//   - List of capability names (e.g., ["SYS_ADMIN", "SYS_RAWIO"])
	GetCapabilities() []string

	// GetDefaultImage returns the default Docker image for this device type.
	//
	// Different device types typically require different container images
	// with device-specific drivers and libraries pre-installed. This method
	// provides a sensible default that can be overridden by users.
	//
	// The method receives device information to determine the appropriate image
	// based on the specific chip model (e.g., Ascend 910B vs 310P) and system
	// architecture (ARM64 vs x86_64).
	//
	// Image Guidelines:
	//   - Use official or verified images when available
	//   - Pin to specific versions for reproducibility
	//   - Include full registry path for clarity
	//
	// Parameters:
	//   - devices: List of devices to get the image for
	//
	// Returns:
	//   - Docker image URL (e.g., "quay.io/ascend/vllm-ascend:v0.11.0rc0")
	//   - Error if image configuration is not found or invalid
	GetDefaultImage(devices []DeviceInfo) (string, error)

	// GetDockerRuntime returns the Docker runtime to use for this device type.
	//
	// Docker supports pluggable runtimes (via OCI spec) that can provide
	// device-specific functionality. Common runtimes:
	//   - "runc": Default OCI runtime (standard containers)
	//   - "nvidia": NVIDIA Container Runtime (automatic GPU setup)
	//   - "kata-runtime": Lightweight VM isolation
	//
	// Returns:
	//   - Runtime name (e.g., "runc", "nvidia", "ascend")
	GetDockerRuntime() string
}

// BoolPtr returns a pointer to a boolean value.
//
// This utility function is useful for Docker API fields that require
// pointer types to distinguish between false and unset.
//
// Parameters:
//   - b: Boolean value to create pointer for
//
// Returns:
//   - Pointer to boolean value
//
// Example:
//   hostConfig.Init = BoolPtr(true)
func BoolPtr(b bool) *bool {
	return &b
}

// GetImageForEngine is a helper function to get Docker image for specific engine.
//
// This function encapsulates the common logic for sandbox implementations to get
// the appropriate Docker image based on device information and engine type. It:
//   1. Extracts chip model name from device list
//   2. Maps model name to configuration key
//   3. Auto-detects system architecture
//   4. Looks up image from RuntimeImagesConfig
//   5. Returns error if any step fails (no fallback)
//
// This is used by both vLLM and MindIE sandbox implementations to avoid code duplication.
//
// Parameters:
//   - runtimeImages: RuntimeImagesConfig instance (as interface{} to avoid import cycle)
//   - devices: List of devices (uses first device's ModelName for chip identification)
//   - engineName: Inference engine name (e.g., "vllm", "mindie")
//
// Returns:
//   - Docker image URL if found
//   - Error if configuration is invalid or image not found
func GetImageForEngine(configMap map[string]map[string]map[string]string, devices []DeviceInfo, engineName string) (string, error) {
	if configMap == nil {
		return "", fmt.Errorf("invalid runtime images configuration")
	}
	
	if len(devices) == 0 {
		return "", fmt.Errorf("no devices provided")
	}
	
	// Use ConfigKey directly (base model config key for image lookup)
	// ConfigKey is always the base model key, not variant_key
	configKey := devices[0].ConfigKey
	if configKey == "" {
		return "", fmt.Errorf("device config key is empty")
	}
	
	// Get image for this chip model and engine (auto-detect architecture)
	// We need to call the GetImageForChipAndEngineAuto logic inline to avoid import cycle
	arch, err := getSystemArch()
	if err != nil {
		return "", fmt.Errorf("failed to detect system architecture: %w", err)
	}
	
	engineMap, ok := configMap[configKey]
	if !ok {
		return "", fmt.Errorf("chip model %s not found in configuration", configKey)
	}
	
	archMap, ok := engineMap[engineName]
	if !ok {
		return "", fmt.Errorf("engine %s not found for chip model %s", engineName, configKey)
	}
	
	image, ok := archMap[arch]
	if !ok {
		return "", fmt.Errorf("architecture %s not found for chip model %s and engine %s", arch, configKey, engineName)
	}
	
	logger.Debug("Selected image for %s (%s): %s", configKey, engineName, image)
	return image, nil
}

// getSystemArch returns the current system architecture
func getSystemArch() (string, error) {
	switch runtimePkg.GOARCH {
	case "arm64", "aarch64":
		return "arm64", nil
	case "amd64", "x86_64":
		return "amd64", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtimePkg.GOARCH)
	}
}

// CheckDockerImageExists checks if a Docker image exists locally using docker CLI.
//
// This function queries Docker to determine if an image is available in the local
// Docker image cache. It uses the docker CLI command for simplicity and compatibility.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - imageName: Full image name (e.g., "ubuntu:20.04", "quay.io/ascend/vllm-ascend:v0.11.0rc0")
//
// Returns:
//   - true if image exists locally
//   - Error if Docker query fails
//
// Thread Safety: Safe for concurrent calls
func CheckDockerImageExists(ctx context.Context, imageName string) (bool, error) {
	if imageName == "" {
		return false, fmt.Errorf("image name cannot be empty")
	}
	
	logger.Debug("Checking if Docker image exists: %s", imageName)
	
	cmd := exec.CommandContext(ctx, "docker", "images", "-q", imageName)
	output, err := cmd.Output()
	
	if err != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("operation cancelled")
		}
		return false, fmt.Errorf("failed to check Docker image: %w", err)
	}
	
	exists := len(strings.TrimSpace(string(output))) > 0
	if exists {
		logger.Debug("Docker image found locally: %s", imageName)
	} else {
		logger.Debug("Docker image not found locally: %s", imageName)
	}
	
	return exists, nil
}

// PullDockerImage pulls a Docker image from registry using docker CLI with PTY.
//
// This function pulls an image from the configured registry (or Docker Hub by default).
// The pull operation shows progress through an optional event channel.
//
// Progress Events:
//   - "DOCKER_CR|..." - Carriage return update (overwrite current line)
//   - "DOCKER_LF|..." - Line feed update (new line)
//   - Regular messages for status updates
//
// The function uses PTY to capture Docker's native progress bars and formatting.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - imageName: Full image name to pull
//   - eventCh: Optional channel for progress events (can be nil)
//
// Returns:
//   - nil on success
//   - Error if pull fails or is cancelled
//
// Thread Safety: Safe for concurrent calls
func PullDockerImage(ctx context.Context, imageName string, eventCh chan<- string) error {
	if imageName == "" {
		return fmt.Errorf("image name cannot be empty")
	}
	
	logger.Info("Pulling Docker image: %s", imageName)
	sendEvent := func(msg string) {
		if eventCh != nil {
			select {
			case eventCh <- msg:
			default:
				// Channel full or closed, skip
			}
		}
	}
	
	sendEvent(fmt.Sprintf("Pulling Docker image: %s", imageName))
	
	cmd := exec.CommandContext(ctx, "docker", "pull", imageName)
	
	// Use PTY to capture native Docker progress bars
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start docker pull with pty: %w", err)
	}
	defer ptmx.Close()
	
	// Read byte by byte to detect \r and \n separately
	var line []byte
	buf := make([]byte, 1)
	
	// Set read deadline to check context periodically
	ptmx.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	
	for {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			// Context cancelled, kill the process
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			return fmt.Errorf("pull operation cancelled")
		default:
		}
		
		n, err := ptmx.Read(buf)
		if n > 0 {
			ch := buf[0]
			
			if ch == '\r' {
				// Carriage return - check if next char is \n (CRLF sequence)
				if len(line) > 0 {
					// Peek at next byte
					next := make([]byte, 1)
					ptmx.SetReadDeadline(time.Now().Add(1 * time.Millisecond))
					nn, _ := ptmx.Read(next)
					ptmx.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
					
					if nn > 0 && next[0] == '\n' {
						// CRLF sequence - treat as newline
						sendEvent("DOCKER_LF|" + string(line))
					} else {
						// Just CR - overwrite current line
						sendEvent("DOCKER_CR|" + string(line))
						// Put back the peeked byte if it's not \n
						if nn > 0 {
							line = append(line[:0], next[0])
							continue
						}
					}
					line = line[:0]
				}
			} else if ch == '\n' {
				// Line feed - send line with LF marker (for new line)
				if len(line) > 0 {
					sendEvent("DOCKER_LF|" + string(line))
					line = line[:0]
				}
			} else {
				line = append(line, ch)
			}
			
			// Reset deadline after successful read
			ptmx.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		}
		
		if err == io.EOF {
			if len(line) > 0 {
				sendEvent("DOCKER_LF|" + string(line))
			}
			break
		}
		if err != nil {
			// Check if it's a timeout error (expected for context checking)
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				// Timeout is expected, continue to next iteration
				ptmx.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				continue
			}
			
			// Other errors might be due to PTY closing when process ends
			// Check if process has exited
			if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
				// Process already exited, PTY closed naturally
				break
			}
			
			// If context was cancelled, this is expected
			if ctx.Err() != nil {
				break
			}
			
			// Real error - but could also be PTY closing, so just break
			// and let cmd.Wait() determine if there was an actual error
			break
		}
	}
	
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("pull operation cancelled")
		}
		return fmt.Errorf("failed to pull image: %w", err)
	}
	
	sendEvent(fmt.Sprintf("Successfully pulled image: %s", imageName))
	logger.Info("Successfully pulled Docker image: %s", imageName)
	
	return nil
}

// EnsureImage checks if an image exists locally and pulls it if not.
//
// This method combines CheckDockerImageExists and PullDockerImage to ensure
// a Docker image is available before creating containers. It's designed to be
// called by concrete runtime implementations (vllm-docker, mindie-docker) in
// their Create methods.
//
// The method sends progress events through the CreateParams.EventChannel:
//   - Checking image availability
//   - Image found/not found status
//   - Pull progress (if needed)
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - imageName: Full image name to ensure
//   - params: CreateParams containing EventChannel for progress updates
//
// Returns:
//   - nil if image is available (either found or pulled successfully)
//   - Error if check fails or pull fails
//
// Thread Safety: Safe for concurrent calls
//
// Example:
//   if err := r.EnsureImage(ctx, imageName, params); err != nil {
//       return nil, fmt.Errorf("failed to ensure image: %w", err)
//   }
func (b *DockerRuntimeBase) EnsureImage(ctx context.Context, imageName string, params *CreateParams) error {
	if imageName == "" {
		return nil // Skip if no image name provided
	}
	
	// Get event channel from params
	var eventCh chan<- string
	if params != nil {
		eventCh = params.EventChannel
	}
	
	sendEvent := func(msg string) {
		if eventCh != nil {
			select {
			case eventCh <- msg:
			default:
				// Channel full or closed, skip
			}
		}
	}
	
	sendEvent("Checking Docker image availability...")
	logger.Debug("Ensuring Docker image is available: %s", imageName)
	
	// Check if image exists locally
	exists, err := CheckDockerImageExists(ctx, imageName)
	if err != nil {
		return fmt.Errorf("failed to check Docker image: %w", err)
	}
	
	if exists {
		sendEvent(fmt.Sprintf("Docker image %s found locally", imageName))
		logger.Debug("Docker image %s already exists locally", imageName)
		return nil
	}
	
	// Image doesn't exist, pull it
	if err := PullDockerImage(ctx, imageName, eventCh); err != nil {
		return fmt.Errorf("failed to pull Docker image: %w", err)
	}
	
	return nil
}

// ApplyTemplateParams applies template parameters from CreateParams to the environment map.
//
// This is a common Docker operation that converts template parameters (key=value format)
// into environment variables (KEY=VALUE format) suitable for Docker containers.
//
// Template parameter keys are converted to environment variable format:
//   - camelCase -> CAMEL_CASE
//   - kebab-case -> KEBAB_CASE
//
// Template environment variables will NOT override existing environment variables.
// This allows explicit environment variables to take precedence over template defaults.
//
// Parameters:
//   - env: Existing environment map to merge template parameters into
//   - params: CreateParams containing TemplateParams to apply
//
// Example:
//   env := map[string]string{"CUSTOM_VAR": "value"}
//   ApplyTemplateParams(env, params) // params.TemplateParams = ["tensorParallel=4"]
//   // Result: env = {"CUSTOM_VAR": "value", "TENSOR_PARALLEL": "4"}
func (b *DockerRuntimeBase) ApplyTemplateParams(env map[string]string, params *CreateParams) {
	if len(params.TemplateParams) == 0 {
		return
	}
	
	logger.Debug("Applying %d template parameter(s) to Docker environment", len(params.TemplateParams))
	
	templateEnv := convertTemplateParamsToEnv(params.TemplateParams)
	
	// Merge template environment variables (template params don't override existing env)
	for k, v := range templateEnv {
		if _, exists := env[k]; !exists {
			env[k] = v
			logger.Debug("Applied template param: %s=%s", k, v)
		} else {
			logger.Debug("Template param %s=%s skipped (already set in environment)", k, v)
		}
	}
}
