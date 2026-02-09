// Package omniinferdocker implements Omni-Infer runtime with Docker deployment.
//
// This package provides a Docker-based runtime for running Omni-Infer inference engine.
// It handles the complete lifecycle of containerized model instances, including:
//   - Container creation with proper device access and mounts
//   - Device-specific configuration via sandbox abstraction
//   - Model serving with Omni-Infer backend
//
// Omni-Infer Features:
//   - Optimized for Ascend NPU inference
//   - Uses host networking for optimal performance
//   - Large shared memory support (500GB default)
//   - Simple API interface
//
// The runtime uses configuration-driven device sandboxes from devices.yaml
// and embeds DockerRuntimeBase for common Docker operations.
package omniinferdocker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"

	"github.com/tsingmaoai/xw-cli/internal/logger"
	"github.com/tsingmaoai/xw-cli/internal/runtime"
)

// Runtime implements the runtime.Runtime interface for Omni-Infer with Docker.
//
// This runtime manages Omni-Infer model instances running in Docker containers.
// Each instance is an isolated container with access to specified hardware devices.
//
// Architecture:
//   - Embeds DockerRuntimeBase for common Docker operations
//   - Uses DeviceSandbox abstraction for device-specific configuration
//   - Implements Create() for Omni-Infer-specific container setup
//   - Uses host networking for optimal performance
//
// Thread Safety:
//   All public methods are thread-safe via inherited mutex protection.
type Runtime struct {
	*runtime.DockerRuntimeBase // Embedded base provides common Docker operations
}

// NewRuntime creates a new Omni-Infer Docker runtime instance.
//
// This function:
//   1. Initializes Docker base with "omni-infer-docker" runtime name
//   2. Verifies Docker daemon connectivity
//   3. Loads any existing containers from previous runs
//
// Returns:
//   - Configured runtime instance ready for use
//   - Error if Docker is unavailable or initialization fails
func NewRuntime() (*Runtime, error) {
	base, err := runtime.NewDockerRuntimeBase("omni-infer-docker")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Docker base: %w", err)
	}

	// CONFIGURATION-DRIVEN STRATEGY: All device sandboxes loaded from devices.yaml
	// No core sandboxes needed - fully configuration-driven
	base.RegisterCoreSandboxes([]func() runtime.DeviceSandbox{})

	rt := &Runtime{
		DockerRuntimeBase: base,
	}

	// Load existing containers from previous runs
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rt.LoadExistingContainers(ctx); err != nil {
		logger.Warn("Failed to load existing Omni-Infer containers: %v", err)
	}

	logger.Info("Omni-Infer Docker runtime initialized successfully (config-driven mode)")

	return rt, nil
}

// Name returns the unique identifier for this runtime.
//
// Returns:
//   - "omni-infer-docker" to distinguish from other implementations
func (r *Runtime) Name() string {
	return "omni-infer-docker"
}

// Create creates a new model instance but does not start it.
//
// This method implements Omni-Infer-specific container creation:
//   1. Validates parameters and checks for duplicate instance IDs
//   2. Selects appropriate device sandbox based on device type
//   3. Prepares device-specific configuration (env, mounts, devices)
//   4. Configures Omni-Infer environment (MODEL_PATH, TENSOR_PARALLEL_SIZE, etc.)
//   5. Sets large shared memory (500GB) for inference
//   6. Uses host networking for optimal performance
//   7. Creates Docker container with all required settings
//   8. Registers instance in runtime's instance map
//
// The created container is in "created" state and must be started separately
// via the Start method (inherited from DockerRuntimeBase).
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

	logger.Info("Creating Omni-Infer Docker instance: %s for model: %s",
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
	// Use ConfigKey (base model) for sandbox selection
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
	env := make(map[string]string)
	for k, v := range params.Environment {
		env[k] = v
	}
	for k, v := range sandboxEnv {
		env[k] = v
	}

	// Apply template parameters from runtime_params.yaml (if any)
	r.ApplyTemplateParams(env, params)

	// Set Omni-Infer-required environment variables
	// MODEL_PATH: Container-internal path where model files are mounted
	env["MODEL_PATH"] = "/mnt/model"

	// MODEL_NAME: Model name used for inference requests
	// Use alias if provided, otherwise use model ID
	modelName := params.Alias
	if modelName == "" {
		modelName = params.ModelID
	}
	env["MODEL_NAME"] = modelName

	// TENSOR_PARALLEL_SIZE: Number of devices for tensor parallelism
	if params.TensorParallel > 0 {
		env["TENSOR_PARALLEL_SIZE"] = fmt.Sprintf("%d", params.TensorParallel)
	} else {
		env["TENSOR_PARALLEL_SIZE"] = fmt.Sprintf("%d", len(params.Devices))
	}

	// MAX_MODEL_LEN: Maximum sequence length (from ExtraConfig or default)
	if maxLen, ok := params.ExtraConfig["max_model_len"].(int); ok && maxLen > 0 {
		env["MAX_MODEL_LEN"] = fmt.Sprintf("%d", maxLen)
	}

	// SERVER_PORT: HTTP server port
	if params.Port > 0 {
		env["SERVER_PORT"] = fmt.Sprintf("%d", params.Port)
	} else {
		env["SERVER_PORT"] = "8000"
	}

	// Convert environment map to slice
	envSlice := make([]string, 0, len(env))
	for k, v := range env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	// Get device mounts from sandbox
	deviceMounts, err := sandbox.GetDeviceMounts(params.Devices)
	if err != nil {
		return nil, fmt.Errorf("failed to get device mounts: %w", err)
	}

	// Convert device paths to Docker device mappings
	deviceMappings := make([]container.DeviceMapping, 0, len(deviceMounts))
	for _, devPath := range deviceMounts {
		deviceMappings = append(deviceMappings, container.DeviceMapping{
			PathOnHost:        devPath,
			PathInContainer:   devPath,
			CgroupPermissions: "rwm",
		})
	}

	// Get additional volume mounts from sandbox
	additionalMounts := sandbox.GetAdditionalMounts()

	// Build mount list
	mounts := []mount.Mount{
		{
			Type:   mount.TypeBind,
			Source: params.ModelPath,
			Target: "/mnt/model",
			ReadOnly: true,
		},
	}

	// Add additional mounts from sandbox
	for hostPath, containerPath := range additionalMounts {
		readOnly := strings.HasSuffix(hostPath, ":ro")
		if readOnly {
			hostPath = strings.TrimSuffix(hostPath, ":ro")
		}

		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   hostPath,
			Target:   containerPath,
			ReadOnly: readOnly,
		})
	}

	// Get Docker image
	imageName, err := sandbox.GetDefaultImage(params.Devices)
	if err != nil {
		return nil, fmt.Errorf("failed to get default image: %w", err)
	}

	// Get shared memory size (default: 500GB for Omni-Infer)
	shmSize := int64(500 * 1024 * 1024 * 1024) // 500GB
	if shmSizer, ok := sandbox.(interface{ GetSharedMemorySize() int64 }); ok {
		shmSize = shmSizer.GetSharedMemorySize()
	}

	// Prepare container configuration
	containerConfig := &container.Config{
		Image: imageName,
		Env:   envSlice,
		Labels: map[string]string{
			"xw.instance_id": params.InstanceID,
			"xw.model_id":    params.ModelID,
		},
	}

	// Prepare host configuration
	hostConfig := &container.HostConfig{
		Mounts:      mounts,
		Resources:   container.Resources{Devices: deviceMappings},
		ShmSize:     shmSize,
		Privileged:  sandbox.RequiresPrivileged(),
		CapAdd:      sandbox.GetCapabilities(),
		Runtime:     sandbox.GetDockerRuntime(),
		NetworkMode: container.NetworkMode("host"), // Use host networking
		RestartPolicy: container.RestartPolicy{
			Name: "unless-stopped",
		},
		Init: func() *bool { b := true; return &b }(), // Enable init for proper signal handling
	}

	// Build device indices string for labeling
	deviceIndices := make([]string, len(params.Devices))
	for i, dev := range params.Devices {
		deviceIndices[i] = fmt.Sprintf("%d", dev.Index)
	}
	deviceIndicesStr := strings.Join(deviceIndices, ",")

	// Container name
	containerName := params.InstanceID
	if params.ServerName != "" {
		containerName = fmt.Sprintf("%s-%s", params.InstanceID, params.ServerName)
	}

	// Prepare extra labels
	extraLabels := map[string]string{
		"xw.device_indices": deviceIndicesStr,
	}

	// Create the container via base method
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
		"shm_size":        fmt.Sprintf("%d", shmSize),
	}

	// Store max concurrent requests if specified
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

	logger.Info("Omni-Infer Docker instance created successfully: %s (container: %s)",
		params.InstanceID, resp.ID[:12])

	return instance, nil
}

