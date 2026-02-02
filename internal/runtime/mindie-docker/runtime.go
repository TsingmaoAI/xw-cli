// Package mindiedocker implements MindIE runtime with Docker deployment.
//
// This package provides a Docker-based runtime for running MindIE inference engine.
// It handles the complete lifecycle of containerized model instances, including:
//   - Container creation with proper device access and extensive mounts
//   - Device-specific configuration via sandbox abstraction
//   - Multi-device distributed inference support
//   - Model serving with MindIE backend
//
// MindIE Features:
//   - Optimized for Ascend NPU distributed inference
//   - Large shared memory support for multi-device communication
//   - Extensive logging and profiling capabilities
//   - Compatible with Huawei CANN ecosystem
//
// The runtime uses device-specific sandboxes to handle chip-specific configurations
// (Ascend NPU, etc.) and embeds DockerRuntimeBase for common Docker operations.
package mindiedocker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"

	"github.com/tsingmaoai/xw-cli/internal/logger"
	"github.com/tsingmaoai/xw-cli/internal/runtime"
)

// Runtime implements the runtime.Runtime interface for MindIE with Docker.
//
// This runtime manages MindIE model instances running in Docker containers.
// Each instance is an isolated container with access to specified hardware devices
// and configured for distributed inference workloads.
//
// Architecture:
//   - Embeds DockerRuntimeBase for common Docker operations
//   - Uses DeviceSandbox abstraction for device-specific configuration
//   - Implements Create() for MindIE-specific container setup
//   - Supports large shared memory for multi-device inference
//
// Thread Safety:
//   All public methods are thread-safe via inherited mutex protection.
type Runtime struct {
	*runtime.DockerRuntimeBase // Embedded base provides common Docker operations
}

// NewRuntime creates a new MindIE Docker runtime instance.
//
// This function:
//   1. Initializes Docker base with "mindie-docker" runtime name
//   2. Verifies Docker daemon connectivity
//   3. Loads any existing containers from previous runs
//
// Returns:
//   - Configured runtime instance ready for use
//   - Error if Docker is unavailable or initialization fails
func NewRuntime() (*Runtime, error) {
	base, err := runtime.NewDockerRuntimeBase("mindie-docker")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Docker base: %w", err)
	}

	rt := &Runtime{
		DockerRuntimeBase: base,
	}

	// Load existing containers from previous runs
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rt.LoadExistingContainers(ctx); err != nil {
		logger.Warn("Failed to load existing MindIE containers: %v", err)
	}

	logger.Info("MindIE Docker runtime initialized successfully")

	return rt, nil
}

// Name returns the unique identifier for this runtime.
//
// Returns:
//   - "mindie-docker" to distinguish from other implementations
func (r *Runtime) Name() string {
	return "mindie-docker"
}

// Create creates a new model instance but does not start it.
//
// This method implements MindIE-specific container creation:
//   1. Validates parameters and checks for duplicate instance IDs
//   2. Selects appropriate device sandbox based on device type
//   3. Prepares device-specific configuration (env, mounts, devices)
//   4. Configures MindIE environment (WORLD_SIZE, device IDs, model lengths)
//   5. Sets large shared memory for distributed inference
//   6. Creates Docker container with all required settings
//   7. Registers instance in runtime's instance map
//
// The created container is in "created" state and must be started separately
// via the Start method (inherited from DockerRuntimeBase).
//
// MindIE-Specific Configuration:
//   - Port: Container internal port 1025 mapped to host port
//   - Shared Memory: 500GB for multi-device communication
//   - Environment Variables:
//     * MINDIE_NPU_DEVICE_IDS: Comma-separated device indices (e.g., "0,1,2,3")
//     * WORLD_SIZE: Number of devices for distributed inference
//     * MODEL_PATH: Container path to model files (/mnt/model)
//     * MODEL_NAME: Model name for inference requests (alias or model ID)
//     * MAX_MODEL_LEN: Maximum sequence length (optional, from ExtraConfig)
//     * MAX_INPUT_LEN: Maximum input length (optional, from ExtraConfig)
//   - Mounts: Extensive log directories for profiling and debugging
//
// Container Configuration:
//   - Image: MindIE image with Ascend support or custom from params.ExtraConfig["image"]
//   - Command: Custom command from params.ExtraConfig["command"] or default entrypoint
//   - Network: Bridge mode with port mapping (container:1025 -> host:params.Port)
//   - Restart: unless-stopped for automatic recovery
//   - Init: Enabled for proper signal handling
//   - ShmSize: 500GB for distributed inference
//
// Labels:
//   Containers are labeled with metadata for discovery and filtering:
//   - xw.runtime: Runtime type (mindie-docker)
//   - xw.model_id: Model identifier
//   - xw.alias: Instance alias for inference
//   - xw.instance_id: Unique instance identifier
//   - xw.backend_type: Backend type (mindie)
//   - xw.deployment_mode: Deployment mode (docker)
//   - xw.device_indices: Comma-separated device indices
//   - xw.server_name: Server identifier for multi-server support
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

	logger.Info("Creating MindIE Docker instance: %s for model: %s",
		params.InstanceID, params.ModelID)

	// Check for duplicate instance ID
	mu := r.GetMutex()
	instances := r.GetInstances()

	mu.RLock()
	if _, exists := instances[params.InstanceID]; exists {
		mu.RUnlock()
		return nil, fmt.Errorf("instance %s already exists", params.InstanceID)
	}
	mu.RUnlock()

	// Validate device requirements
	if len(params.Devices) == 0 {
		return nil, fmt.Errorf("at least one device is required")
	}

	// Select device sandbox based on device type by querying all registered sandboxes
	var sandbox MindIESandbox
	deviceType := string(params.Devices[0].Type)
	
	// Try each registered sandbox until we find one that supports this device type
	for _, sandboxConstructor := range sandboxRegistry {
		sb := sandboxConstructor()
		if sb.Supports(deviceType) {
			sandbox = sb
			logger.Debug("Selected sandbox for device type %s: %T", deviceType, sandbox)
			break
		}
	}
	
	if sandbox == nil {
		return nil, fmt.Errorf("no sandbox found for device type: %s", deviceType)
	}

	// Prepare sandbox-specific environment variables
	sandboxEnv, err := sandbox.PrepareEnvironment(params.Devices)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare environment: %w", err)
	}

	// Merge user environment with sandbox environment
	// Sandbox environment takes precedence for device-specific variables
	env := make(map[string]string)
	for k, v := range params.Environment {
		env[k] = v
	}
	for k, v := range sandboxEnv {
		env[k] = v
	}
	
	// Apply template parameters from runtime_params.yaml (if any)
	// Template params are converted to environment variables
	r.ApplyTemplateParams(env, params)

	// Set MindIE-required environment variables
	// MODEL_PATH: Container-internal path where model files are mounted
	env["MODEL_PATH"] = "/mnt/model"
	
	// MODEL_NAME: Model name used for inference requests
	// Use instance alias if set, otherwise use model ID
	modelName := params.Alias
	if modelName == "" {
		modelName = params.ModelID
	}
	env["MODEL_NAME"] = modelName

	// Configure SERVER_PORT environment variable for MindIE
	// MindIE will listen on the port specified by SERVER_PORT (default: 8000)
	env["SERVER_PORT"] = "8000"

	// Use unified parallelism parameters from Manager
	// WORLD_SIZE: Set by Manager (TENSOR_PARALLEL * PIPELINE_PARALLEL)
	if params.WorldSize > 0 {
		env["WORLD_SIZE"] = fmt.Sprintf("%d", params.WorldSize)
	}

	// TENSOR_PARALLEL: Set by Manager (optional, for engines that support it)
	if params.TensorParallel > 0 {
		env["TENSOR_PARALLEL"] = fmt.Sprintf("%d", params.TensorParallel)
	}

	// Convert environment map to Docker format (KEY=VALUE strings)
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}

	// Configure port mapping for inference API
	// Use port 8000 internally (same as vLLM) for consistency
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}

	if params.Port > 0 {
		// Map container port 8000 to host port
		containerPort := nat.Port("8000/tcp")
		exposedPorts[containerPort] = struct{}{}
		portBindings[containerPort] = []nat.PortBinding{
			{
				HostIP:   "127.0.0.1", // Bind to localhost only for security
				HostPort: fmt.Sprintf("%d", params.Port),
			},
		}
	}

	// Determine Docker image to use
	// Priority: params.ExtraConfig["image"] > device-specific default
	var imageName string
	if img, ok := params.ExtraConfig["image"]; ok {
		if imgStr, ok := img.(string); ok {
			imageName = imgStr
			logger.Info("Using custom Docker image: %s", imageName)
		}
	}
	
	if imageName == "" {
		// Get image from configuration
		var err error
		imageName, err = sandbox.GetDefaultImage(params.Devices)
		if err != nil {
			return nil, fmt.Errorf("failed to get Docker image: %w", err)
		}
		logger.Info("Using configured Docker image: %s", imageName)
	}

	// Ensure Docker image is available (check and pull if needed)
	if err := r.EnsureImage(ctx, imageName, params); err != nil {
		return nil, fmt.Errorf("failed to ensure Docker image: %w", err)
	}

	// Determine MindIE command to execute
	// Priority: params.ExtraConfig["command"] > default container entrypoint
	var cmd []string
	if cmdInterface, ok := params.ExtraConfig["command"]; ok {
		if cmdSlice, ok := cmdInterface.([]string); ok {
			cmd = cmdSlice
		}
	}

	// If no custom command, let the container use its default entrypoint
	// MindIE containers typically have a built-in start script

	// Prepare device indices string for container labels
	deviceIndicesStr := ""
	if len(params.Devices) > 0 {
		indices := make([]string, len(params.Devices))
		for i, dev := range params.Devices {
			indices[i] = fmt.Sprintf("%d", dev.Index)
		}
		deviceIndicesStr = strings.Join(indices, ",")
	}

	// Prepare container labels for discovery and filtering
	labels := map[string]string{
		"xw.runtime":         r.Name(),
		"xw.model_id":        params.ModelID,
		"xw.alias":           params.Alias,
		"xw.instance_id":     params.InstanceID,
		"xw.backend_type":    params.BackendType,
		"xw.deployment_mode": params.DeploymentMode,
		"xw.device_indices":  deviceIndicesStr,
		"xw.server_name":     params.ServerName,
	}

	// Build container configuration
	containerConfig := &container.Config{
		Image:        imageName,
		Env:          envList,
		Cmd:          cmd, // May be nil to use default entrypoint
		ExposedPorts: exposedPorts,
		Tty:          false,
		OpenStdin:    true,  // Enable interactive mode for debugging
		AttachStdin:  true,  // Attach stdin for interactive shells
		Labels:       labels,
	}

	// Get device-specific device mounts (e.g., /dev/davinci0)
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
	// Model mount is always included, device-specific mounts are added
	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   params.ModelPath,
			Target:   "/mnt/model",
			ReadOnly: true, // Model files are read-only for safety
		},
	}

	// Add device-specific mounts (driver libs, tools, logs, cache)
	additionalMounts := sandbox.GetAdditionalMounts()
	for src, dst := range additionalMounts {
		// Determine read-only vs writable based on mount purpose
		// Log directories and cache need write access
		readOnly := !isWritableMount(dst)
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   src,
			Target:   dst,
			ReadOnly: readOnly,
		})
	}

	// Get shared memory size for distributed inference
	// MindIE requires large shared memory (typically 500GB) for multi-device communication
	shmSize := sandbox.GetSharedMemorySize()

	// Build host configuration with MindIE-specific settings
	hostConfig := &container.HostConfig{
		Resources: container.Resources{
			Devices: devices, // Device access (e.g., NPUs)
		},
		Mounts:       mounts,
		PortBindings: portBindings,
		NetworkMode:  "bridge",
		Privileged:   sandbox.RequiresPrivileged(), // Required for NPU access
		Runtime:      sandbox.GetDockerRuntime(),   // Device-specific runtime (e.g., "runc")
		Init:         runtime.BoolPtr(true),        // Use init for proper signal handling
		ShmSize:      shmSize,                      // Large shared memory for distributed inference
		RestartPolicy: container.RestartPolicy{
			Name: "no", // No auto-restart, instance lifecycle managed by xw server
		},
	}

	// Build container name with server suffix for multi-server support
	containerName := params.InstanceID
	if params.ServerName != "" {
		containerName = fmt.Sprintf("%s-%s", params.InstanceID, params.ServerName)
	}

	// Create the container via Docker API
	cli := r.GetDockerClient()
	resp, err := cli.ContainerCreate(
		ctx,
		containerConfig,
		hostConfig,
		nil, // Network config (use default)
		nil, // Platform config (use default)
		containerName,
	)
	if err != nil {
		return nil, err
	}

	// Build instance metadata
	metadata := map[string]string{
		"container_id":    resp.ID,
		"image":           imageName,
		"device_type":     string(deviceType),
		"backend_type":    params.BackendType,
		"deployment_mode": params.DeploymentMode,
		"shm_size":        fmt.Sprintf("%d", shmSize),
	}

	// Store max concurrent requests if specified (used by proxy for concurrency control)
	if maxConcurrent, ok := params.ExtraConfig["max_concurrent"].(int); ok && maxConcurrent > 0 {
		metadata["max_concurrent"] = fmt.Sprintf("%d", maxConcurrent)
	}

	// Create instance structure
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

	// Register instance in tracking map
	mu.Lock()
	instances[params.InstanceID] = instance
	mu.Unlock()

	logger.Info("MindIE Docker instance created successfully: %s (container: %s)",
		params.InstanceID, resp.ID[:12])

	return instance, nil
}

// MindIESandbox extends the base DeviceSandbox interface with MindIE-specific methods.
//
// This interface adds MindIE-specific functionality on top of the standard
// DeviceSandbox interface, particularly for shared memory configuration.
type MindIESandbox interface {
	runtime.DeviceSandbox
	
	// GetSharedMemorySize returns the shared memory size required for MindIE.
	//
	// Returns:
	//   - Shared memory size in bytes
	GetSharedMemorySize() int64
	
	// Supports checks if this sandbox supports the given device type.
	//
	// Parameters:
	//   - deviceType: Device type string (e.g., "ascend-910b", "ascend-310p")
	//
	// Returns:
	//   - true if this sandbox supports the device type
	Supports(deviceType string) bool
}

// sandboxRegistry holds all registered sandbox implementations
var sandboxRegistry = []func() MindIESandbox{
	func() MindIESandbox { return NewAscendSandbox() },
	// Add more sandbox constructors here as new chips are supported
}

// isWritableMount determines if a mount path requires write access.
//
// MindIE containers need write access to:
//   - Log directories for runtime logs, profiling, and dumps
//   - Cache directory for model downloads and compilation artifacts
//
// All other mounts (driver libs, tools, system binaries) are read-only
// for security and stability.
//
// Parameters:
//   - path: Container mount path
//
// Returns:
//   - true if mount needs write access
func isWritableMount(path string) bool {
	writablePaths := []string{
		"/var/log/npu/slog",      // System logs
		"/var/log/npu/profiling", // Profiling data
		"/var/log/npu/dump",      // Core dumps
		"/usr/slog",              // Legacy log path
		"/root/.cache",           // Cache directory
	}

	for _, wp := range writablePaths {
		if path == wp {
			return true
		}
	}

	return false
}

