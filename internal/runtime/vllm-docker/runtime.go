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

	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/runtime"
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
//   2. Verifies Docker daemon connectivity
//   3. Loads any existing containers from previous runs
//
// Returns:
//   - Configured runtime instance ready for use
//   - Error if Docker is unavailable or initialization fails
func NewRuntime() (*Runtime, error) {
	base, err := runtime.NewDockerRuntimeBase("vllm-docker")
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
		logger.Warn("Failed to load existing vLLM containers: %v", err)
	}
	
	logger.Info("vLLM Docker runtime initialized successfully")
	
	return rt, nil
}

// Name returns the unique identifier for this runtime.
//
// Returns:
//   - "vllm-docker" to distinguish from other implementations
func (r *Runtime) Name() string {
	return "vllm-docker"
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
	
	// Select device sandbox based on device type
	var sandbox runtime.DeviceSandbox
	deviceType := params.Devices[0].Type
	
	switch deviceType {
	case api.DeviceTypeAscend:
		sandbox = NewAscendSandbox()
	default:
		return nil, fmt.Errorf("unsupported device type: %s", deviceType)
	}
	
	// Prepare device-specific environment variables
	deviceEnv, err := sandbox.PrepareEnvironment(params.Devices)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare environment: %w", err)
	}
	
	// Merge user environment with device environment
	// Device environment takes precedence for device-specific variables
	env := make(map[string]string)
	for k, v := range params.Environment {
		env[k] = v
	}
	for k, v := range deviceEnv {
		env[k] = v
	}
	
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
				HostIP:   "0.0.0.0", // Bind to all interfaces
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
	
	// Determine vLLM command to execute
	// Priority: params.ExtraConfig["command"] > default vLLM serve command
	var cmd []string
	if cmdInterface, ok := params.ExtraConfig["command"]; ok {
		if cmdSlice, ok := cmdInterface.([]string); ok {
			cmd = cmdSlice
		}
	}
	
	// Use default vLLM command if not provided
	if cmd == nil {
		// Use instance ID (set to alias in manager) as the served model name
		// This allows clients to reference the model by its alias in inference requests
		cmd = []string{
			"vllm",
			"serve",
			"/mnt/model",
			"--served-model-name",
			params.InstanceID,
		}
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
		Cmd:          cmd,
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
		RestartPolicy: container.RestartPolicy{
			Name: "unless-stopped", // Auto-restart unless explicitly stopped
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
		return nil, fmt.Errorf("failed to create Docker container: %w", err)
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
