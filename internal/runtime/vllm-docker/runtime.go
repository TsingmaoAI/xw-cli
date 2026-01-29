// Package vllmdocker implements vLLM runtime with Docker deployment.
//
// This package provides a Docker-based runtime for running vLLM inference engine.
// It handles the complete lifecycle of containerized model instances, including:
//   - Container creation with proper device access and mounts
//   - Container lifecycle management (start, stop, remove)
//   - Instance state tracking and monitoring
//   - Log streaming
//
// The runtime uses device-specific sandboxes to handle chip-specific configurations.
package vllmdocker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/runtime"
)

// Runtime implements the runtime.Runtime interface for vLLM with Docker.
//
// This runtime manages vLLM model instances running in Docker containers.
// Each instance is an isolated container with access to specified hardware devices.
//
// Thread-safety: All public methods are thread-safe via mutex protection.
type Runtime struct {
	client     *client.Client                // Docker client for container operations
	mu         sync.RWMutex                   // Protects instances map
	instances  map[string]*runtime.Instance   // Active instances indexed by ID
	serverName string                         // Server unique identifier for filtering
}

// NewRuntime creates a new vLLM Docker runtime instance.
//
// This function:
//   1. Initializes Docker client with API version negotiation
//   2. Verifies Docker daemon connectivity
//   3. Loads any existing containers from previous runs
//
// Returns:
//   - Configured runtime instance
//   - Error if Docker is unavailable or initialization fails
func NewRuntime() (*Runtime, error) {
	// Create Docker client with environment-based configuration
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	
	// Verify Docker daemon is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("Docker daemon is not accessible: %w", err)
	}
	
	rt := &Runtime{
		client:    cli,
		instances: make(map[string]*runtime.Instance),
	}
	
	// Load existing containers from previous runs
	if err := rt.loadExistingContainers(context.Background()); err != nil {
		logger.Warn("Failed to load existing vLLM containers: %v", err)
	}
	
	logger.Info("vLLM Docker runtime initialized successfully")
	
	return rt, nil
}

// Name returns the unique identifier for this runtime.
//
// The name "vllm-docker" distinguishes this runtime from other implementations
// like "vllm-native" or "mindie-docker".
func (r *Runtime) Name() string {
	return "vllm-docker"
}

// SetServerName sets the server name for this runtime (for multi-server support)
func (r *Runtime) SetServerName(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.serverName = name
}

// ReloadContainers reloads existing containers from Docker
func (r *Runtime) ReloadContainers(ctx context.Context) error {
	// Clear existing instances
	r.mu.Lock()
	r.instances = make(map[string]*runtime.Instance)
	r.mu.Unlock()
	
	// Reload with current server name filter
	return r.loadExistingContainers(ctx)
}

// Create creates a new model instance but does not start it.
//
// This method:
//   1. Validates parameters and checks for duplicate instance IDs
//   2. Selects appropriate device sandbox based on device type
//   3. Prepares device-specific configuration (env, mounts, devices)
//   4. Creates Docker container with all required settings
//   5. Registers instance in runtime's instance map
//
// The created container is in "created" state and must be started separately
// via the Start method.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - params: Standard creation parameters including model info and devices
//
// Returns:
//   - Instance metadata with container information
//   - Error if creation fails at any step
func (r *Runtime) Create(ctx context.Context, params *runtime.CreateParams) (*runtime.Instance, error) {
	if params == nil || params.InstanceID == "" {
		return nil, fmt.Errorf("invalid parameters: instance ID is required")
	}
	
	logger.Info("Creating vLLM Docker instance: %s for model: %s", 
		params.InstanceID, params.ModelID)
	
	// Check for duplicate instance ID
	r.mu.RLock()
	if _, exists := r.instances[params.InstanceID]; exists {
		r.mu.RUnlock()
		return nil, fmt.Errorf("instance %s already exists", params.InstanceID)
	}
	r.mu.RUnlock()
	
	// Select device sandbox based on device type
	if len(params.Devices) == 0 {
		return nil, fmt.Errorf("at least one device is required")
	}
	
	var sandbox DeviceSandbox
	deviceType := params.Devices[0].Type
	
	switch deviceType {
	case api.DeviceTypeAscend:
		sandbox = NewAscendSandbox()
	default:
		return nil, fmt.Errorf("unsupported device type: %s", deviceType)
	}
	
	// Prepare device-specific environment
	deviceEnv, err := sandbox.PrepareEnvironment(params.Devices)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare environment: %w", err)
	}
	
	// Merge user environment with device environment
	env := make(map[string]string)
	for k, v := range params.Environment {
		env[k] = v
	}
	for k, v := range deviceEnv {
		env[k] = v
	}
	
	// Convert environment map to list
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}
	
	// Configure port mapping
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}
	
	if params.Port > 0 {
		// vLLM listens on port 8000 inside container
		containerPort := nat.Port("8000/tcp")
		exposedPorts[containerPort] = struct{}{}
		portBindings[containerPort] = []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: fmt.Sprintf("%d", params.Port),
			},
		}
	}
	
	// Get Docker image from extra config or use default
	// Note: Image should already be pulled by hooks before this method is called
	imageName := "quay.io/ascend/vllm-ascend:v0.11.0rc0"
	if img, ok := params.ExtraConfig["image"]; ok {
		if imgStr, ok := img.(string); ok {
			imageName = imgStr
		}
	}
	
	logger.Info("Using Docker image: %s", imageName)
	
	// Get container command from extra config or use default vLLM command
	var cmd []string
	if cmdInterface, ok := params.ExtraConfig["command"]; ok {
		if cmdSlice, ok := cmdInterface.([]string); ok {
			cmd = cmdSlice
		}
	}
	
	// If no command provided, use default vLLM command
	if cmd == nil {
		// Use instance ID (which is set to alias in manager) as the served model name for inference
		cmd = []string{
			"vllm",
			"serve",
			"/mnt/model",
			"--served-model-name",
			params.InstanceID,
		}
	}
	
	// Prepare device indices string for container label
	deviceIndicesStr := ""
	if len(params.Devices) > 0 {
		indices := make([]string, len(params.Devices))
		for i, dev := range params.Devices {
			indices[i] = fmt.Sprintf("%d", dev.Index)
		}
		deviceIndicesStr = strings.Join(indices, ",")
	}
	
	// Prepare labels
	labels := map[string]string{
		"xw.runtime":         r.Name(),
		"xw.model_id":        params.ModelID,
		"xw.alias":           params.Alias,          // Store alias for inference
		"xw.instance_id":     params.InstanceID,
		"xw.backend_type":    params.BackendType,    // Store backend type
		"xw.deployment_mode": params.DeploymentMode, // Store deployment mode
		"xw.device_indices":  deviceIndicesStr,      // Store allocated device indices
		"xw.server_name":     params.ServerName,     // Store server name for multi-server support
	}
	
	// Build container configuration
	containerConfig := &container.Config{
		Image:        imageName,
		Env:          envList,
		Cmd:          cmd,
		ExposedPorts: exposedPorts,
		// Don't specify User - let image use its default user
		Tty:          false,
		OpenStdin:    true,       // Enable interactive mode
		AttachStdin:  true,       // Attach stdin
		Labels:       labels,
	}
	
	// Get device mounts from sandbox
	deviceMounts, err := sandbox.GetDeviceMounts(params.Devices)
	if err != nil {
		return nil, fmt.Errorf("failed to get device mounts: %w", err)
	}
	
	// Convert device paths to Docker device mappings
	devices := make([]container.DeviceMapping, 0, len(deviceMounts))
	for _, devPath := range deviceMounts {
		devices = append(devices, container.DeviceMapping{
			PathOnHost:        devPath,
			PathInContainer:   devPath,
			CgroupPermissions: "rwm", // Read, write, and mknod permissions
		})
	}
	
	// Build volume mounts
	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   params.ModelPath,
			Target:   "/mnt/model",
			ReadOnly: true, // Model files are read-only for safety
		},
	}
	
	// Add device-specific mounts
	additionalMounts := sandbox.GetAdditionalMounts()
	for src, dst := range additionalMounts {
		// Cache directory needs write access, others are read-only
		readOnly := (dst != "/root/.cache")
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   src,
			Target:   dst,
			ReadOnly: readOnly,
		})
	}
	
	// Build host configuration
	// Explicitly specify "runc" runtime to avoid Docker auto-selecting ascend-docker-runtime
	// We access Ascend NPUs via device mounts + privileged mode using standard runtime
	hostConfig := &container.HostConfig{
		Resources: container.Resources{
			Devices: devices,
		},
		Mounts:       mounts,
		PortBindings: portBindings,
		NetworkMode:  "bridge",
		Privileged:   sandbox.RequiresPrivileged(),
		Runtime:      "runc", // Use standard OCI runtime, not ascend-docker-runtime
		Init:         boolPtr(true), // Use init for proper signal handling
		RestartPolicy: container.RestartPolicy{
			Name: "unless-stopped", // Auto-restart unless explicitly stopped
		},
	}
	
	// Build container name with server suffix for multi-server support
	containerName := params.InstanceID
	if params.ServerName != "" {
		containerName = fmt.Sprintf("%s-%s", params.InstanceID, params.ServerName)
	}
	
	// Create the container
	resp, err := r.client.ContainerCreate(
		ctx,
		containerConfig,
		hostConfig,
		nil, // Network config
		nil, // Platform config
		containerName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker container: %w", err)
	}
	
	// Build instance metadata
	metadata := map[string]string{
		"container_id":    resp.ID,
		"image":           imageName,
		"device_type":     string(deviceType),
		"backend_type":    params.BackendType,    // Store backend type
		"deployment_mode": params.DeploymentMode, // Store deployment mode
	}
	
	// Store max concurrent requests if specified (used by proxy for concurrency control)
	if maxConcurrent, ok := params.ExtraConfig["max_concurrent"].(int); ok && maxConcurrent > 0 {
		metadata["max_concurrent"] = fmt.Sprintf("%d", maxConcurrent)
	}
	
	instance := &runtime.Instance{
		ID:           params.InstanceID,
		RuntimeName:  r.Name(),
		CreatedAt:    time.Now(),
		ModelID:      params.ModelID,
		Alias:        params.Alias,
		ModelVersion: params.ModelVersion,
		State:        runtime.StateCreated,
		Port:         params.Port,
		Endpoint:     fmt.Sprintf("http://localhost:%d", params.Port),
		Metadata:     metadata,
	}
	
	// Register instance
	r.mu.Lock()
	r.instances[params.InstanceID] = instance
	r.mu.Unlock()
	
	logger.Info("vLLM Docker instance created successfully: %s (container: %s)", 
		params.InstanceID, resp.ID[:12])
	
	return instance, nil
}

// Start starts a created instance.
//
// This method starts the Docker container associated with the instance.
// The container will begin executing its entrypoint/command and load the model.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - instanceID: ID of the instance to start
//
// Returns:
//   - Error if instance not found or Docker operation fails
func (r *Runtime) Start(ctx context.Context, instanceID string) error {
	r.mu.RLock()
	instance, exists := r.instances[instanceID]
	r.mu.RUnlock()
	
	if !exists {
		return fmt.Errorf("instance not found: %s", instanceID)
	}
	
	containerID := instance.Metadata["container_id"]
	if containerID == "" {
		return fmt.Errorf("container ID not found for instance: %s", instanceID)
	}
	
	logger.Info("Starting vLLM Docker instance: %s", instanceID)
	
	if err := r.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	
	// Update instance state
	r.mu.Lock()
	instance.State = runtime.StateRunning
	instance.StartedAt = time.Now()
	r.mu.Unlock()
	
	logger.Info("vLLM Docker instance started successfully: %s", instanceID)
	
	return nil
}

// Stop stops a running instance.
//
// This method gracefully stops the container with a 15-minute timeout.
// If the container doesn't stop within the timeout, it will be forcefully killed.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - instanceID: ID of the instance to stop
//
// Returns:
//   - Error if instance not found or Docker operation fails
func (r *Runtime) Stop(ctx context.Context, instanceID string) error {
	r.mu.RLock()
	instance, exists := r.instances[instanceID]
	r.mu.RUnlock()
	
	if !exists {
		return fmt.Errorf("instance not found: %s", instanceID)
	}
	
	containerID := instance.Metadata["container_id"]
	logger.Info("Stopping vLLM Docker instance: %s", instanceID)
	
	// Stop with 15-minute grace period (900 seconds)
	timeout := 900
	stopOptions := container.StopOptions{Timeout: &timeout}
	
	if err := r.client.ContainerStop(ctx, containerID, stopOptions); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}
	
	// Update state to stopped (keep instance and container for later restart or removal)
		r.mu.Lock()
		instance.State = runtime.StateStopped
		instance.StoppedAt = time.Now()
		r.mu.Unlock()
	
	logger.Info("vLLM Docker instance stopped successfully: %s (container kept for restart or removal)", instanceID)
	
	return nil
}

// Remove removes an instance and its container.
//
// This method stops (if running) and removes the container.
// The instance is unregistered from the runtime.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - instanceID: ID of the instance to remove
//
// Returns:
//   - Error if instance not found or Docker operation fails
func (r *Runtime) Remove(ctx context.Context, instanceID string) error {
	r.mu.RLock()
	instance, exists := r.instances[instanceID]
	r.mu.RUnlock()
	
	if !exists {
		return fmt.Errorf("instance not found: %s", instanceID)
	}
	
	containerID := instance.Metadata["container_id"]
	logger.Info("Removing vLLM Docker instance: %s", instanceID)
	
	removeOptions := container.RemoveOptions{
		Force:         true, // Force remove even if running
		RemoveVolumes: true, // Clean up anonymous volumes
	}
	
	if err := r.client.ContainerRemove(ctx, containerID, removeOptions); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}
	
	// Unregister instance
	r.mu.Lock()
	delete(r.instances, instanceID)
	r.mu.Unlock()
	
	logger.Info("vLLM Docker instance removed successfully: %s", instanceID)
	
	return nil
}

// Get retrieves instance information.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - instanceID: ID of the instance to retrieve
//
// Returns:
//   - Instance metadata
//   - Error if instance not found
func (r *Runtime) Get(ctx context.Context, instanceID string) (*runtime.Instance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	instance, exists := r.instances[instanceID]
	if !exists {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}
	
	return instance, nil
}

// List returns all instances managed by this runtime.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//
// Returns:
//   - List of all instances
//   - Error (currently always nil, reserved for future use)
func (r *Runtime) List(ctx context.Context) ([]*runtime.Instance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	instances := make([]*runtime.Instance, 0, len(r.instances))
	for _, inst := range r.instances {
		instances = append(instances, inst)
	}
	
	return instances, nil
}

// Logs retrieves container logs for an instance.
//
// This method streams logs from the Docker container. If follow is true,
// the stream remains open and continues to receive new log entries.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - instanceID: ID of the instance
//   - follow: If true, stream logs continuously; if false, return existing logs
//
// Returns:
//   - LogStream for reading logs
//   - Error if instance not found or Docker operation fails
func (r *Runtime) Logs(ctx context.Context, instanceID string, follow bool) (runtime.LogStream, error) {
	r.mu.RLock()
	instance, exists := r.instances[instanceID]
	r.mu.RUnlock()
	
	if !exists {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}
	
	containerID := instance.Metadata["container_id"]
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Timestamps: true,
		Tail:       "all",
	}
	
	reader, err := r.client.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get container logs: %w", err)
	}
	
	return &logStream{reader: reader}, nil
}

// loadExistingContainers loads containers from previous runs.
//
// This method queries Docker for containers labeled with this runtime's name
// and registers them in the instances map. This allows the runtime to resume
// managing containers after a restart.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//
// Returns:
//   - Error if Docker query fails
func (r *Runtime) loadExistingContainers(ctx context.Context) error {
	// Query for containers with our runtime label
	containers, err := r.client.ContainerList(ctx, container.ListOptions{
		All: true, // Include stopped containers
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("xw.runtime=%s", r.Name())),
		),
	})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}
	
	// Get global port allocator
	portAllocator := runtime.GetGlobalPortAllocator()
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for _, c := range containers {
		instanceID := c.Labels["xw.instance_id"]
		if instanceID == "" {
			continue
		}
		
		// Filter by server name if set (for multi-server support)
		if r.serverName != "" {
			containerServerName := c.Labels["xw.server_name"]
			if containerServerName != r.serverName {
				continue // Skip containers from other servers
			}
		}
		
		// Determine instance state from container state
		state := runtime.StateStopped
		if c.State == "running" {
			state = runtime.StateRunning
		}
		
		// Extract port mapping from container
		// The container exposes port 8000, we need to find the host port it's mapped to
		port := 0
		for _, portMapping := range c.Ports {
			if portMapping.PrivatePort == 8000 {
				port = int(portMapping.PublicPort)
				break
			}
		}
		
		// If no port was found, log a warning
		// This means the container was created without port mapping and cannot be accessed via proxy
		if port == 0 {
			logger.Warn("Container %s has no port mapping - it cannot be accessed via proxy API. "+
				"This may happen if the instance was created without specifying a port. "+
				"Consider recreating the instance with port allocation.", instanceID)
		} else {
			// Mark the port as used to prevent conflicts with new instances
			portAllocator.MarkPortUsed(port)
		}
		
		// Convert container creation time (Unix timestamp) to time.Time
		createdAt := time.Unix(c.Created, 0)
		
		// For StartedAt, we need to inspect the container to get accurate start time
		// For now, use CreatedAt as a fallback (better than zero value)
		startedAt := createdAt
		
		// Try to get more accurate start time from container inspection
		if state == runtime.StateRunning {
			if inspectData, err := r.client.ContainerInspect(ctx, c.ID); err == nil {
				if inspectData.State != nil && inspectData.State.StartedAt != "" {
					if parsedTime, err := time.Parse(time.RFC3339Nano, inspectData.State.StartedAt); err == nil {
						startedAt = parsedTime
					}
				}
			}
		}
		
		// Prepare metadata
		metadata := map[string]string{
			"container_id":    c.ID,
			"backend_type":    c.Labels["xw.backend_type"],    // Read backend type from label
			"deployment_mode": c.Labels["xw.deployment_mode"], // Read deployment mode from label
		}
		
		instance := &runtime.Instance{
			ID:          instanceID,
			RuntimeName: r.Name(),
			ModelID:     c.Labels["xw.model_id"],
			Alias:       c.Labels["xw.alias"],
			State:       state,
			Port:        port,
			CreatedAt:   createdAt,
			StartedAt:   startedAt,
			Metadata:    metadata,
		}
		
		r.instances[instanceID] = instance
		logger.Info("Loaded existing vLLM instance: %s (state: %s, port: %d, created: %s)", 
			instanceID, state, port, createdAt.Format(time.RFC3339))
	}
	
	return nil
}

// logStream implements runtime.LogStream for Docker container logs.
type logStream struct {
	reader io.ReadCloser
}

func (s *logStream) Read(p []byte) (n int, err error) {
	return s.reader.Read(p)
}

func (s *logStream) Close() error {
	return s.reader.Close()
}

// DeviceSandbox defines the interface for device-specific configuration.
//
// Each device type (Ascend, Kunlun, etc.) implements this interface to provide
// chip-specific Docker configuration.
type DeviceSandbox interface {
	PrepareEnvironment(devices []runtime.DeviceInfo) (map[string]string, error)
	GetDeviceMounts(devices []runtime.DeviceInfo) ([]string, error)
	GetAdditionalMounts() map[string]string
	RequiresPrivileged() bool
	GetCapabilities() []string
}

// boolPtr returns a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
}


