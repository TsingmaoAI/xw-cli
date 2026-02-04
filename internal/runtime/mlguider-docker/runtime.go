// Package mlguiderdocker implements MLGuider runtime with Docker deployment.
//
// MLGuider is a high-performance inference engine optimized for large language models
// on Huawei Ascend NPUs. This package provides Docker-based deployment with support for:
//   - Multi-device tensor parallelism and expert parallelism
//   - Dynamic model path management (original and converted models)
//   - Automatic device allocation and environment configuration
//   - OpenAI-compatible API endpoints
//
// Architecture:
//   - Uses host networking for optimal performance (--net=host)
//   - Requires Ascend driver mounted from host system
//   - Supports multi-NPU distributed inference via WORLD_SIZE and device arrays
//   - Exposes port 8000 for OpenAI-compatible inference API
//
// Container Requirements:
//   - Ascend driver mounted at /usr/local/Ascend/driver
//   - Model directory mounted (supports both original and converted models)
//   - Privileged mode for hardware access
//
// Environment Variables:
//   - ORIGIN_MODEL_PATH: Path to original model files
//   - MODEL_PATH: Path to converted/optimized model files
//   - MAX_MODEL_LEN: Maximum sequence length for inference
//   - TENSOR_PARALLEL: Number of devices for tensor parallelism
//   - EXPERT_PARALLEL: Number of devices for expert parallelism (MoE models)
//   - WORLD_SIZE: Total number of NPU devices (should equal TENSOR_PARALLEL * EXPERT_PARALLEL)
//   - DEVICES: JSON array of device indices (e.g., "[0,1,2,3]")
//   - API_PORT: HTTP server port (default: 8000)
//   - MODEL_NAME: Model name for API requests
package mlguiderdocker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"

	"github.com/tsingmaoai/xw-cli/internal/logger"
	"github.com/tsingmaoai/xw-cli/internal/runtime"
)

// Runtime implements the Runtime interface for MLGuider with Docker deployment.
//
// MLGuider Runtime Architecture:
//   - Embeds DockerRuntimeBase for common Docker operations (start, stop, logs, etc.)
//   - Implements Create() to configure MLGuider-specific containers
//   - Uses AscendSandbox for device-specific parameter transformation
//   - Supports multi-device distributed inference with automatic configuration
//
// Container Lifecycle:
//  1. Create: Configures container with MLGuider-specific environment and mounts
//  2. Start: Launches the container (via base.Start)
//  3. Running: Container exposes OpenAI-compatible API on port 8000
//  4. Stop: Gracefully stops the container (via base.Stop)
//  5. Remove: Cleans up container and releases resources (via base.Remove)
//
// Thread Safety: All operations are thread-safe through DockerRuntimeBase mutex
type Runtime struct {
	*runtime.DockerRuntimeBase
}

// sandboxRegistry holds all registered sandbox implementations for MLGuider
var sandboxRegistry = []func() runtime.DeviceSandbox{
	func() runtime.DeviceSandbox { return NewAscendSandbox() },
	// Add more sandbox constructors here as new chips are supported
}

// NewRuntime creates and initializes a new MLGuider Docker runtime.
//
// Initialization Steps:
//  1. Creates Docker runtime base with "mlguider-docker" identifier
//  2. Verifies Docker daemon connectivity
//  3. Loads any existing MLGuider containers from previous sessions
//  4. Validates Ascend driver availability (warning only, not fatal)
//
// The runtime is immediately ready to create and manage MLGuider instances.
// Existing containers are automatically tracked and restored to the instance map.
//
// Returns:
//   - Configured Runtime instance ready for use
//   - Error if Docker daemon is unreachable or initialization fails
//
// Example:
//
//	rt, err := NewRuntime()
//	if err != nil {
//	    return nil, fmt.Errorf("failed to initialize MLGuider runtime: %w", err)
//	}
func NewRuntime() (*Runtime, error) {
	base, err := runtime.NewDockerRuntimeBase("mlguider-docker")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Docker base: %w", err)
	}

	rt := &Runtime{
		DockerRuntimeBase: base,
	}

	// Load existing containers to restore state after server restart
	if err := rt.LoadExistingContainers(context.Background()); err != nil {
		logger.Warn("Failed to load existing MLGuider containers: %v", err)
		// Non-fatal: we can still create new instances
	}

	logger.Info("MLGuider Docker runtime initialized successfully")
	return rt, nil
}

// Create creates a new MLGuider model instance with Docker deployment.
//
// This method orchestrates the complete container creation process:
//  1. Validates parameters (instance ID, devices, model path)
//  2. Selects appropriate device sandbox based on hardware type
//  3. Prepares device-specific environment variables (DEVICES, WORLD_SIZE, etc.)
//  4. Configures MLGuider-specific environment (MODEL_PATH, TENSOR_PARALLEL, etc.)
//  5. Sets up volume mounts for drivers, models, and system binaries
//  6. Creates Docker container with host networking and privileged mode
//  7. Registers instance in tracking map
//
// MLGuider-Specific Configuration:
//   - Uses host network mode (--net=host) for optimal performance
//   - Mounts Ascend driver from /usr/local/Ascend/driver
//   - Configures multi-device parallelism based on device count
//   - Supports both original and converted model paths
//   - Sets up OpenAI-compatible API on port 8000
//
// Parallelism Strategy:
//   - Single device: No parallelism (TENSOR_PARALLEL=1)
//   - Multiple devices: Tensor parallelism across all devices
//   - MoE models: Can be configured with EXPERT_PARALLEL via ExtraConfig
//
// Container Labels:
//   - xw.runtime: mlguider-docker
//   - xw.instance_id: Unique instance identifier
//   - xw.model_id: Model identifier from registry
//   - xw.alias: Instance alias for API requests
//   - xw.backend_type: mlguider
//   - xw.deployment_mode: docker
//   - xw.device_indices: Comma-separated device indices
//   - xw.server_name: Server identifier for multi-server setups
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - params: Standard creation parameters including model info, devices, and config
//
// Returns:
//   - Instance metadata with container information and port assignment
//   - Error if creation fails at any step
//
// Example ExtraConfig:
//
//	{
//	  "image": "harbor.tsingmao.com/mlguider/release:0123-xw",
//	  "max_model_len": 66000,
//	  "tensor_parallel": 2,
//	  "expert_parallel": 1
//	}
func (r *Runtime) Create(ctx context.Context, params *runtime.CreateParams) (*runtime.Instance, error) {
	if params == nil || params.InstanceID == "" {
		return nil, fmt.Errorf("invalid parameters: instance ID is required")
	}

	logger.Info("Creating MLGuider Docker instance: %s for model: %s",
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
		return nil, fmt.Errorf("at least one device is required for MLGuider")
	}

	// Select device sandbox based on device type by querying all registered sandboxes
	var sandbox runtime.DeviceSandbox
	// Use ConfigKey (base model) for sandbox selection, not Type (which may be variant_key)
	deviceType := params.Devices[0].ConfigKey
	
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
	// This includes DEVICES array and NPU-specific configuration
	sandboxEnv, err := sandbox.PrepareEnvironment(params.Devices)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare sandbox environment: %w", err)
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

	// Configure MLGuider-specific environment variables
	// These control model loading, parallelism, and inference behavior

	// ORIGIN_MODEL_PATH: Original model directory (e.g., HuggingFace format)
	env["ORIGIN_MODEL_PATH"] = params.ModelPath

	// MODEL_PATH: Converted/optimized model directory under data directory
	// MLGuider will convert the model if this directory doesn't exist
	// Multiple instances can share the same converted model directory
	// Path structure: {dataDir}/tmp/{modelID}
	convertedModelDir, err := ensureMLGuiderModelDir(params.DataDir, params.ModelID)
	if err != nil {
		return nil, fmt.Errorf("failed to create MLGuider model directory: %w", err)
	}
	env["MODEL_PATH"] = convertedModelDir

	// MAX_MODEL_LEN: Maximum sequence length for inference
	// Default: 8192, configurable via ExtraConfig
	maxModelLen := 8192
	if configLen, ok := params.ExtraConfig["max_model_len"].(int); ok && configLen > 0 {
		maxModelLen = configLen
	}
	env["MAX_MODEL_LEN"] = fmt.Sprintf("%d", maxModelLen)

	// Use unified parallelism parameters from Manager
	// TENSOR_PARALLEL: Set by Manager (defaults to device count)
	if params.TensorParallel > 0 {
		env["TENSOR_PARALLEL"] = fmt.Sprintf("%d", params.TensorParallel)
	}

	// EXPERT_PARALLEL: For MoE models, use ExtraConfig if provided
	// Default: 1 (no expert parallelism)
	expertParallel := 1
	if configEP, ok := params.ExtraConfig["expert_parallel"].(int); ok && configEP > 0 {
		expertParallel = configEP
		env["EXPERT_PARALLEL"] = fmt.Sprintf("%d", expertParallel)
	}

	// WORLD_SIZE: Set by Manager (TENSOR_PARALLEL * PIPELINE_PARALLEL)
	if params.WorldSize > 0 {
		env["WORLD_SIZE"] = fmt.Sprintf("%d", params.WorldSize)
	}

	// API_PORT: HTTP server port for inference API
	// MLGuider exposes OpenAI-compatible API on this port
	env["API_PORT"] = "8000"

	// MODEL_NAME: Model name for API requests
	// Use instance alias if set, otherwise use model ID
	modelName := params.Alias
	if modelName == "" {
		modelName = params.ModelID
	}
	env["MODEL_NAME"] = modelName

	// Convert environment map to Docker format (KEY=VALUE strings)
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}

	// Configure port mapping for inference API
	// MLGuider listens on port 8000 inside container
	// Map it to host port specified in params.Port for external access
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}

	if params.Port > 0 {
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
		imageName, _ = img.(string)
	}

	// Get default image from sandbox if not specified
	if imageName == "" {
		var err error
		imageName, err = sandbox.GetDefaultImage(params.Devices)
		if err != nil {
			return nil, fmt.Errorf("failed to get Docker image: %w", err)
		}
	}

	logger.Info("Using MLGuider Docker image: %s", imageName)

	// Prepare container labels for discovery and filtering
	// These labels enable instance tracking and multi-server support
	labels := map[string]string{
		"xw.runtime":          "mlguider-docker",
		"xw.instance_id":      params.InstanceID,
		"xw.model_id":         params.ModelID,
		"xw.alias":            params.Alias,
		"xw.backend_type":     params.BackendType,
		"xw.deployment_mode":  params.DeploymentMode,
		"xw.server_name":      params.ServerName,
	}

	// Add device indices to labels for debugging and monitoring
	if len(params.Devices) > 0 {
		deviceIndices := make([]string, len(params.Devices))
		for i, dev := range params.Devices {
			deviceIndices[i] = fmt.Sprintf("%d", dev.Index)
		}
		labels["xw.device_indices"] = strings.Join(deviceIndices, ",")
	}

	// Get device mounts for direct hardware access
	// These include /dev/davinci*, /dev/davinci_manager, etc.
	deviceMountPaths, err := sandbox.GetDeviceMounts(params.Devices)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare device mounts: %w", err)
	}

	// Convert device paths to Docker device mappings
	deviceMounts := make([]container.DeviceMapping, 0, len(deviceMountPaths))
	for _, devPath := range deviceMountPaths {
		deviceMounts = append(deviceMounts, container.DeviceMapping{
			PathOnHost:        devPath,
			PathInContainer:   devPath,
			CgroupPermissions: "rwm", // Read, write, and mknod permissions
		})
	}

	// Build volume mounts
	// Original model mount (read-only) and converted model mount (read-write) are required
	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   params.ModelPath,
			Target:   params.ModelPath, // Original model path for ORIGIN_MODEL_PATH env var
			ReadOnly: true,              // Original model files are read-only
		},
	}

	// Add converted model directory mount
	// MLGuider converts the original model to optimized format in this directory
	// The directory is shared across multiple instances of the same model
	// If conversion already exists, MLGuider will skip conversion and load directly
	// Path: {dataDir}/tmp/{modelID}
	mounts = append(mounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   convertedModelDir,
		Target:   convertedModelDir, // Converted model path for MODEL_PATH env var
		ReadOnly: false,             // Read-write access required for model conversion
	})

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

	// Determine container name
	// Format: {runtime}-{instance_id}-{server_name}
	// Example: mlguider-qwen3-32b-abc123
	containerName := fmt.Sprintf("%s-%s", r.Name(), params.InstanceID)
	if params.ServerName != "" {
		containerName = fmt.Sprintf("%s-%s", containerName, params.ServerName)
	}

	// Create container configuration
	containerConfig := &container.Config{
		Image:        imageName,
		Env:          envList,
		Cmd:          nil, // Use image default entrypoint
		ExposedPorts: exposedPorts,
		Labels:       labels,
		Tty:          false,
		OpenStdin:    true,  // Enable interactive mode for debugging
		AttachStdin:  true,  // Attach stdin for interactive shells
	}

	// Get shared memory size for inference workloads
	// MLGuider requires adequate shared memory for DataLoader workers and model tensor sharing
	var shmSize int64 = 16 * 1024 * 1024 * 1024 // Default 16GB
	if shmProvider, ok := sandbox.(interface{ GetSharedMemorySize() int64 }); ok {
		shmSize = shmProvider.GetSharedMemorySize()
	}

	// Create host configuration with networking, devices, and security settings
	hostConfig := &container.HostConfig{
		// Use bridge networking with port mapping for isolation and security
		// Maps container port 8000 to host port specified in params.Port
		NetworkMode: "bridge",
		
		// Port bindings for inference API
		PortBindings: portBindings,
		
		// Volume mounts for drivers, models, and system binaries
		Mounts: mounts,
		
		// Device resources including NPU hardware access
		Resources: container.Resources{
			Devices: deviceMounts,
		},
		
		// Privileged mode required for Ascend driver interaction
		Privileged: true,
		
		// Shared memory for DataLoader and model tensor sharing
		ShmSize: shmSize,
		
		// Restart policy: never restart automatically
		// Instance lifecycle is managed by xw server
		RestartPolicy: container.RestartPolicy{
			Name: "no",
		},
	}

	// Create the Docker container
	logger.Debug("Creating MLGuider container: %s", containerName)
	cli := r.GetDockerClient()
	resp, err := cli.ContainerCreate(
		ctx,
		containerConfig,
		hostConfig,
		nil, // Network config (not used with host networking)
		nil, // Platform config
		containerName,
	)
	if err != nil {
		return nil, err
	}

	logger.Info("Created MLGuider container: %s (ID: %s)", containerName, resp.ID[:12])

	// Create instance metadata
	instance := &runtime.Instance{
		ID:          params.InstanceID,
		RuntimeName: r.Name(),
		ModelID:     params.ModelID,
		Alias:       params.Alias,
		State:       runtime.StateCreated,
		Port:        params.Port,
		CreatedAt:   time.Now(),
		Metadata: map[string]string{
			"container_id":      resp.ID,
			"container_name":    containerName,
			"backend_type":      params.BackendType,
			"deployment_mode":   params.DeploymentMode,
			"image":             imageName,
			"tensor_parallel":   fmt.Sprintf("%d", params.TensorParallel),
			"expert_parallel":   fmt.Sprintf("%d", expertParallel),
			"world_size":        fmt.Sprintf("%d", params.WorldSize),
			"max_model_len":     fmt.Sprintf("%d", maxModelLen),
		},
	}

	// Store max concurrent requests if specified (used by proxy for concurrency control)
	if maxConcurrent, ok := params.ExtraConfig["max_concurrent"].(int); ok && maxConcurrent > 0 {
		instance.Metadata["max_concurrent"] = fmt.Sprintf("%d", maxConcurrent) 
	}

	// Register instance in tracking map
	mu.Lock()
	instances[params.InstanceID] = instance
	mu.Unlock()

	logger.Info("MLGuider instance created successfully: %s", params.InstanceID)
	return instance, nil
}

// Name returns the runtime name identifier.
//
// This name is used for:
//   - Runtime registration and discovery
//   - Container labeling and filtering
//   - Logging and monitoring
//
// Returns: "mlguider-docker"
func (r *Runtime) Name() string {
	return "mlguider-docker"
}

// ensureMLGuiderModelDir creates the MLGuider converted model directory if it doesn't exist.
//
// MLGuider converts and optimizes models for efficient inference. The converted models
// are stored in {dataDir}/tmp/{modelID} on the host. This directory:
//   - Persists across container restarts
//   - Is shared between multiple instances of the same model
//   - Is created with 0755 permissions for read/write/execute access
//   - Is located parallel to the models directory under the data directory
//   - Is not automatically cleaned up (user must manually delete if needed)
//
// If the directory already exists, this function does nothing (idempotent).
//
// Parameters:
//   - dataDir: Data directory root path (e.g., "~/.xw/data")
//   - modelID: Model identifier (e.g., "qwen2-0.5b")
//
// Returns:
//   - Absolute path to the created/existing directory
//   - error if directory creation fails or path exists but is not a directory
func ensureMLGuiderModelDir(dataDir, modelID string) (string, error) {
	// Build the converted model directory path under data directory
	// Path structure: {dataDir}/tmp/{modelID}
	convertedDir := filepath.Join(dataDir, "tmp", modelID)
	
	// Check if directory already exists
	if info, err := os.Stat(convertedDir); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("%s exists but is not a directory", convertedDir)
		}
		// Directory exists, nothing to do
		logger.Debug("MLGuider model directory already exists: %s", convertedDir)
		return convertedDir, nil
	}
	
	// Create directory with parent directories
	// 0755 permissions: owner rwx, group rx, others rx
	if err := os.MkdirAll(convertedDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", convertedDir, err)
	}
	
	logger.Info("Created MLGuider model directory: %s", convertedDir)
	return convertedDir, nil
}

