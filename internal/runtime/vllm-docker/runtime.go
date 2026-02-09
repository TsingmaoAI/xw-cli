// Package vllmdocker implements vLLM runtime with Docker deployment.
//
// This package provides a Docker-based runtime for running vLLM inference engine.
// It handles the complete lifecycle of containerized model instances, including:
//   - Container creation with proper device access and mounts
//   - Device-specific configuration via sandbox abstraction
//   - Instance state tracking and monitoring
//   - Model serving with vLLM backend
//
// The runtime uses device-specific sandboxes to handle chip-specific configurations
// (Ascend NPU, etc.) and embeds DockerRuntimeBase for common Docker operations.
package vllmdocker

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

// Runtime implements the runtime.Runtime interface for vLLM with Docker.
//
// This runtime manages vLLM model instances running in Docker containers.
// Each instance is an isolated container with access to specified hardware devices.
//
// Architecture:
//   - Embeds DockerRuntimeBase for common Docker operations
//   - Uses DeviceSandbox abstraction for device-specific configuration
//   - Implements Create() for vLLM-specific container setup
//
// Thread Safety:
//   All public methods are thread-safe via inherited mutex protection.
type Runtime struct {
	*runtime.DockerRuntimeBase // Embedded base provides common Docker operations
}

// NewRuntime creates a new vLLM Docker runtime instance.
//
// This function:
//   1. Initializes Docker base with "vllm-docker" runtime name
//   2. Registers core sandbox implementations
//   3. Verifies Docker daemon connectivity
//   4. Loads any existing containers from previous runs
//
// Returns:
//   - Configured runtime instance ready for use
//   - Error if Docker is unavailable or initialization fails
func NewRuntime() (*Runtime, error) {
	base, err := runtime.NewDockerRuntimeBase("vllm:docker")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Docker base: %w", err)
	}
	
	// CONFIGURATION-DRIVEN STRATEGY: All device sandboxes now loaded from devices.yaml
	// Core sandboxes are kept for reference but not registered (commented out below)
	// This allows configuration-only upgrades without recompiling binaries
	//
	// Legacy core sandbox registration (DISABLED):
	// base.RegisterCoreSandboxes([]func() runtime.DeviceSandbox{
	// 	func() runtime.DeviceSandbox { return NewAscendSandbox() },
	// 	func() runtime.DeviceSandbox { return NewMetaXSandbox() },
	// })
	//
	// New approach: Extended sandboxes from devices.yaml take precedence
	// All device configurations (Ascend, MetaX, etc.) are now in configs/devices.yaml
	base.RegisterCoreSandboxes([]func() runtime.DeviceSandbox{})
	
	rt := &Runtime{
		DockerRuntimeBase: base,
	}
	
	// Load existing containers from previous runs
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rt.LoadExistingContainers(ctx); err != nil {
		logger.Warn("Failed to load existing vLLM containers: %v", err)
	}
	
	logger.Info("vLLM Docker runtime initialized successfully (config-driven mode)")
	
	return rt, nil
}

// Name returns the unique identifier for this runtime.
//
// Returns:
//   - "vllm:docker" to distinguish from other implementations
func (r *Runtime) Name() string {
	return "vllm:docker"
}

// Create creates a new model instance but does not start it.
//
// This method implements vLLM-specific container creation:
//   1. Validates parameters and checks for duplicate instance IDs
//   2. Selects appropriate device sandbox based on device type
//   3. Prepares device-specific configuration (env, mounts, devices)
//   4. Configures vLLM command with model path and serving options
//   5. Creates Docker container with all required settings
//   6. Registers instance in runtime's instance map
//
// The created container is in "created" state and must be started separately
// via the Start method (inherited from DockerRuntimeBase).
//
// Container Configuration:
//   - Image: Device-specific vLLM image or custom from params.ExtraConfig["image"]
//   - Command: vLLM serve with model path and instance alias
//   - Network: Bridge mode with port mapping (container:8000 -> host:params.Port)
//   - Restart: unless-stopped for automatic recovery
//   - Init: Enabled for proper signal handling
//
// Labels:
//   Containers are labeled with metadata for discovery and filtering:
//   - xw.runtime: Runtime type (vllm-docker)
//   - xw.model_id: Model identifier
//   - xw.alias: Instance alias for inference
//   - xw.instance_id: Unique instance identifier
//   - xw.backend_type: Backend type (vllm)
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
	
	logger.Info("Creating vLLM Docker instance: %s for model: %s", 
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
	
	// Select device sandbox using unified selection logic from base
	// This automatically handles configuration-first priority: extended sandboxes (config) > core sandboxes (code)
	// Use ConfigKey (base model) for sandbox selection, not Type (which may be variant_key)
	deviceType := params.Devices[0].ConfigKey
	sandbox, err := r.SelectSandbox(deviceType)
	if err != nil {
		return nil, err
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
	
	// Set parallelism parameters (only if specified by user)
	// These control tensor parallelism across multiple devices
	if params.TensorParallel > 0 {
		env["TENSOR_PARALLEL"] = fmt.Sprintf("%d", params.TensorParallel)
		logger.Debug("Set TENSOR_PARALLEL=%d", params.TensorParallel)
	}
	if params.WorldSize > 0 {
		env["WORLD_SIZE"] = fmt.Sprintf("%d", params.WorldSize)
		logger.Debug("Set WORLD_SIZE=%d", params.WorldSize)
	}
	
	// MODEL_PATH: Container-internal path where model files are mounted
	env["MODEL_PATH"] = "/mnt/model"
	
	// MODEL_NAME: Model name used for inference requests
	// Use instance alias if set, otherwise use model ID
	modelName := params.Alias
	if modelName == "" {
		modelName = params.ModelID
	}
	env["MODEL_NAME"] = modelName
	logger.Debug("Set MODEL_NAME=%s", modelName)
	
	// Convert environment map to Docker format (KEY=VALUE strings)
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}
	
	// Configure port mapping for inference API
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}
	
	if params.Port > 0 {
		// vLLM serves on port 8000 inside container
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
	
	// Prepare device indices string for container labels
	deviceIndicesStr := ""
	if len(params.Devices) > 0 {
		indices := make([]string, len(params.Devices))
		for i, dev := range params.Devices {
			indices[i] = fmt.Sprintf("%d", dev.Index)
		}
		deviceIndicesStr = strings.Join(indices, ",")
	}
	
	// Build container configuration
	// Cmd is not set - use the default CMD from Docker image
	containerConfig := &container.Config{
		Image:        imageName,
		Env:          envList,
		ExposedPorts: exposedPorts,
		Tty:          false,
		OpenStdin:    true,  // Enable interactive mode for debugging
		AttachStdin:  true,  // Attach stdin for interactive shells
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
	
	// Add device-specific mounts (driver libs, tools, cache)
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
	
	// Get shared memory size for inference workloads
	// vLLM requires adequate shared memory for DataLoader workers and KV cache management
	var shmSize int64 = 16 * 1024 * 1024 * 1024 // Default 16GB
	if shmProvider, ok := sandbox.(interface{ GetSharedMemorySize() int64 }); ok {
		shmSize = shmProvider.GetSharedMemorySize()
	}

	// Build host configuration with device-specific settings
	hostConfig := &container.HostConfig{
		Resources: container.Resources{
			Devices: devices, // Device access (e.g., NPUs)
		},
		Mounts:       mounts,
		PortBindings: portBindings,
		NetworkMode:  "bridge",
		Privileged:   sandbox.RequiresPrivileged(), // May require privileged mode for device access
		Runtime:      sandbox.GetDockerRuntime(),   // Device-specific runtime (e.g., "runc")
		Init:         runtime.BoolPtr(true),        // Use init for proper signal handling
		ShmSize:      shmSize,                      // Shared memory for DataLoader and KV cache
		RestartPolicy: container.RestartPolicy{
			Name: "no", // No auto-restart, instance lifecycle managed by xw server
		},
	}
	
	// Build container name with server suffix for multi-server support
	containerName := params.InstanceID
	if params.ServerName != "" {
		containerName = fmt.Sprintf("%s-%s", params.InstanceID, params.ServerName)
	}
	
	// Prepare vLLM-specific labels
	extraLabels := map[string]string{
		"xw.device_indices": deviceIndicesStr,
	}
	
	// Create the container via base method (automatically adds common labels)
	resp, err := r.CreateContainerWithLabels(ctx, params, containerConfig, hostConfig, containerName, extraLabels)
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
	
	logger.Info("vLLM Docker instance created successfully: %s (container: %s)", 
		params.InstanceID, resp.ID[:12])
	
	return instance, nil
}
