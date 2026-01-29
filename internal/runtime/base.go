// Package runtime provides runtime management for model instances.
package runtime

import (
	"context"
	"fmt"
	"io"
	runtimePkg "runtime"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/tsingmao/xw/internal/device"
	"github.com/tsingmao/xw/internal/logger"
)

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
	instance.State = StateRunning
	instance.StartedAt = time.Now()
	b.mu.Unlock()

	logger.Info("Docker container started successfully: %s", instanceID)

	return nil
}

// Stop gracefully stops a running Docker container.
//
// This method sends SIGTERM to the container and waits up to 15 minutes
// for graceful shutdown. If the container doesn't stop within the timeout,
// Docker will send SIGKILL to force termination.
//
// The extended timeout (900 seconds) allows models to complete any in-flight
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

	// Configure graceful shutdown with 15-minute timeout
	timeout := 900
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
// Parameters:
//   - ctx: Context for cancellation (currently unused, reserved for future use)
//   - instanceID: Unique identifier of the instance to retrieve
//
// Returns:
//   - Instance pointer on success
//   - Error if instance not found
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) Get(ctx context.Context, instanceID string) (*Instance, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	instance, exists := b.instances[instanceID]
	if !exists {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}

	return instance, nil
}

// List returns all instances managed by this runtime.
//
// The returned slice contains pointers to all tracked instances, regardless
// of their state (running, stopped, etc.). The list is a snapshot at call time.
//
// Parameters:
//   - ctx: Context for cancellation (currently unused, reserved for future use)
//
// Returns:
//   - Slice of instance pointers (empty if no instances)
//   - Error (currently always nil, reserved for future use)
//
// Thread Safety: Safe for concurrent calls
func (b *DockerRuntimeBase) List(ctx context.Context) ([]*Instance, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	instances := make([]*Instance, 0, len(b.instances))
	for _, inst := range b.instances {
		instances = append(instances, inst)
	}

	return instances, nil
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
//   3. Extracting container metadata and port mappings
//   4. Marking allocated ports as used to prevent conflicts
//   5. Registering instances in the tracking map
//
// This allows the runtime to resume managing containers after a restart,
// enabling seamless server upgrades and crash recovery.
//
// Container State Mapping:
//   - Docker "running" -> StateRunning
//   - Docker "created", "exited", etc. -> StateStopped
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

		// Map Docker container state to instance state
		state := StateStopped
		if c.State == "running" {
			state = StateRunning
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
		if state == StateRunning {
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
			State:       state,
			Port:        port,
			CreatedAt:   createdAt,
			StartedAt:   startedAt,
			Metadata:    metadata,
		}

		b.instances[instanceID] = instance
		loadedCount++

		logger.Info("Loaded existing container: %s (instance: %s, state: %s, port: %d)",
			c.ID[:12], instanceID, state, port)
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
	
	// Get chip model name from first device
	chipModelName := devices[0].ModelName
	if chipModelName == "" {
		return "", fmt.Errorf("device model name is empty")
	}
	
	// Map chip model name to configuration key using device package
	configKey, err := device.GetConfigKeyByModelName(chipModelName)
	if err != nil {
		return "", fmt.Errorf("failed to get config key: %w", err)
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
	
	logger.Debug("Selected image for %s (%s): %s", chipModelName, engineName, image)
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

